pub use imp::run_futex;
pub use imp::run_sleep;
pub use imp::run_socket;
pub use imp::run_socket_async;
pub use imp::run_yield;

mod exec;

mod imp {
    use async_trait::async_trait;
    use core::panic;
    use std::{
        io::{Read, Write},
        os::unix::net::UnixStream,
        sync::{
            atomic::{AtomicBool, Ordering},
            Arc,
        },
    };
    use tokio;

    // A runner provides methods (and state) to run one iteration and to gracefully
    // cancel/terminate execution.
    // on_cancelled must exit quickly (in time/blocking no greater than one
    // iteration), without panicking, and ensuring that no deadlock occurs.  This is
    // especially important where Runners are linked to each other, e.g., in an echo
    // pattern.
    trait Runner {
        fn one_iteration(&mut self);
        fn on_cancelled(&mut self);
    }

    // Run the number of threads with the provided termination flag.
    // each thread will be provided a runner constructed from runner_generator
    //
    // R: the generated runner is a Runner, can Send to another thread
    // F: the runner generator takes a thread_id and produces R
    fn run<F, R>(num_threads: u16, term: &AtomicBool, runner_generator: F) -> u64
    where
        R: Runner + std::marker::Send,
        F: Fn(u16) -> R,
    {
        return std::thread::scope(|scope| {
            let mut handles: Vec<std::thread::ScopedJoinHandle<u64>> =
                Vec::with_capacity(num_threads as usize);
            for thread_id in 0..num_threads {
                let mut thread_runner = runner_generator(thread_id);
                handles.push(scope.spawn(move || {
                    let mut count: u64 = 0;
                    loop {
                        if term.load(Ordering::Relaxed) {
                            thread_runner.on_cancelled();
                            return count;
                        }
                        thread_runner.one_iteration();
                        count += 1;
                    }
                }));
            }

            let mut total_count: u64 = 0;
            for handle in handles {
                let count_from_thread = handle.join().unwrap();
                total_count += count_from_thread;
            }
            return total_count;
        });
    }

    struct YieldRunner {}
    impl Runner for YieldRunner {
        fn one_iteration(self: &mut YieldRunner) {
            self.yield_or_panic();
        }
        fn on_cancelled(self: &mut YieldRunner) {}
    }
    impl YieldRunner {
        fn yield_or_panic(&self) {
            let errno: i32;
            let sys_ret: libc::c_int;
            unsafe {
                *libc::__errno_location() = 0;
                sys_ret = libc::sched_yield();
                errno = *libc::__errno_location();
            }
            match (sys_ret, errno) {
                (0, 0) => {}
                (ret, err) => panic!("sched_yield. ret: {}. errno: {}", ret, err),
            }
        }
    }

    pub fn run_yield(num_threads: u16, term: &AtomicBool) -> u64 {
        return run(num_threads, term, |_| {
            return YieldRunner {};
        });
    }

    struct SleepRunner {
        thread_sleep_ns: u64,
    }
    impl Runner for SleepRunner {
        fn one_iteration(self: &mut SleepRunner) {
            self.sleep_or_panic();
        }
        fn on_cancelled(self: &mut SleepRunner) {}
    }
    impl SleepRunner {
        fn sleep_or_panic(&self) {
            let errno: i32;
            let sys_ret: libc::c_int;
            unsafe {
                *libc::__errno_location() = 0;
                let ts: libc::timespec = libc::timespec {
                    tv_sec: 0,
                    tv_nsec: (self.thread_sleep_ns as i64),
                };
                sys_ret = libc::nanosleep(&ts, std::ptr::null_mut());
                errno = *libc::__errno_location();
            }
            match (sys_ret, errno) {
                (0, 0) => {}
                (-1, libc::EINTR) => {}
                (ret, err) => panic!("nanosleep. ret: {}. errno: {}", ret, err),
            }
        }
    }

    pub fn run_sleep(num_threads: u16, sleep_ns: &u64, term: &AtomicBool) -> u64 {
        return run(num_threads, term, |_| {
            return SleepRunner {
                thread_sleep_ns: *sleep_ns,
            };
        });
    }

    type MyFutex = linux_futex::Futex<linux_futex::Private>;
    struct FutexRunner<'a> {
        futex1: &'a MyFutex,
        futex2: &'a MyFutex,
        is_first_in_pair: bool,
    }
    impl<'a> FutexRunner<'a> {
        fn wait_on_futex(futex: &MyFutex) {
            loop {
                match futex.wait(0) {
                    Err(linux_futex::WaitError::Interrupted) => continue,
                    _ => break,
                }
            }
            futex.value.store(0, Ordering::Release);
        }

        fn wake_futex(futex: &MyFutex) {
            futex.value.store(1, Ordering::Release);
            futex.wake(1);
        }
    }
    impl<'a> Runner for FutexRunner<'a> {
        fn one_iteration(&mut self) {
            if self.is_first_in_pair {
                FutexRunner::wake_futex(self.futex2);
                FutexRunner::wait_on_futex(&self.futex1);
            } else {
                FutexRunner::wait_on_futex(&self.futex2);
                FutexRunner::wake_futex(self.futex1);
            }
        }
        fn on_cancelled(&mut self) {
            // force the other to wake up no matter which we are.
            FutexRunner::wake_futex(self.futex1);
            FutexRunner::wake_futex(self.futex2);
        }
    }

    pub fn run_futex(num_pairs: u16, term: &AtomicBool) -> u64 {
        let num_threads = num_pairs * 2;
        let futexes: Vec<MyFutex> = (0..num_threads)
            .map(|_| linux_futex::Futex::new(0))
            .collect();

        return run(num_threads, term, |thread_id| {
            let is_first_in_pair = thread_id % 2 == 0;
            let base_idx = if is_first_in_pair {
                thread_id
            } else {
                thread_id - 1
            } as usize;
            return FutexRunner {
                futex1: &futexes[base_idx],
                futex2: &futexes[base_idx + 1],
                is_first_in_pair,
            };
        });
    }

    struct SocketRunner<'a> {
        socket: &'a UnixStream,
        // first in pair sends first, 2nd receives first
        is_first_in_pair: bool,
        buf: [u8; 1],
    }
    impl<'a> SocketRunner<'a> {
        fn write_one_byte(&mut self) {
            let result = self.socket.write(&self.buf);
            match result {
                Ok(_) => {}
                Err(e) if e.kind() == std::io::ErrorKind::Interrupted => {}
                Err(e) => {
                    panic!("unix socket write error: {}", e)
                }
            }
        }

        fn read_one_byte(&mut self) {
            let result = self.socket.read(&mut self.buf);
            match result {
                Ok(_) => {}
                Err(e) if e.kind() == std::io::ErrorKind::Interrupted => {}
                Err(e) => {
                    panic!("unix socket read error: {}", e)
                }
            }
        }
    }
    impl<'a> Runner for SocketRunner<'a> {
        fn one_iteration(&mut self) {
            if self.is_first_in_pair {
                self.write_one_byte();
                self.read_one_byte();
            } else {
                self.read_one_byte();
                self.write_one_byte();
            }
        }
        fn on_cancelled(&mut self) {
            // send 1 byte in case the peer didn't catch the term flag.
            // ignore any error in case it saw the term flag and closed the socket.
            let _ = self.socket.write(&self.buf);
        }
    }

    pub fn run_socket(num_pairs: u16, term: &AtomicBool) -> u64 {
        let num_threads = num_pairs * 2;
        // adjacent sockets whose indices / 2 (trunc) produce the same value are
        // connected to each other
        let sockets: Vec<UnixStream> = (0..num_pairs)
            .map(|_| UnixStream::pair().expect("error creating unix socket pair"))
            .flat_map(|pair| [pair.0, pair.1])
            .collect();

        return run(num_threads, term, |thread_id| {
            let is_first_in_pair = thread_id % 2 == 0;
            let socket = &sockets[thread_id as usize];
            return SocketRunner {
                socket,
                is_first_in_pair,
                buf: [0],
            };
        });
    }

    fn create_async_runtime() -> tokio::runtime::Runtime {
        tokio::runtime::Builder::new_current_thread()
            .build()
            .expect("creating runtime")
    }

    #[async_trait]
    trait AsyncRunner {
        async fn one_iteration(&mut self);
        async fn on_cancelled(&mut self);
    }

    // runner is a mutex simply to be able to Send it
    async fn run_task_async(term: Arc<AtomicBool>, mut runner: Box<dyn AsyncRunner>) -> u64 {
        let mut count: u64 = 0;
        loop {
            {
                if term.load(Ordering::Relaxed) {
                    runner.on_cancelled().await;
                    break;
                }

                runner.one_iteration().await;
                count += 1;
            }
        }
        return count;
    }

    fn run_async(term: Arc<AtomicBool>, runners: Vec<Box<dyn AsyncRunner>>) -> u64 {
        // tasks are spawned into a local set (so tasks need not be Send), and
        // into a join set so more easily await them.
        let local_set = tokio::task::LocalSet::new();
        let mut join_set: tokio::task::JoinSet<u64> = tokio::task::JoinSet::new();
        for runner in runners {
            let term_arc = term.clone();
            join_set.spawn_local_on(run_task_async(term_arc, runner), &local_set);
        }

        let runtime = create_async_runtime();
        local_set.block_on(&runtime, async move {
            let mut result: Result<u64, tokio::task::JoinError> = Ok(0);
            while let Some(joined) = join_set.join_next().await {
                match (&result, joined) {
                    // already have an error
                    (Err(_), _) => continue,
                    (Ok(_), Err(e)) => result = Err(e),
                    (Ok(c1), Ok(c2)) => result = Ok(c1 + c2),
                };
            }
            result.expect("join error")
        })
    }

    struct AsyncSocket1 {
        stream: UnixStream,
        buf: [u8; 1],
        notify1: Arc<tokio::sync::Notify>,
        notify2: Arc<tokio::sync::Notify>,
        cancel_barrier: Arc<tokio::sync::Barrier>,
    }

    #[async_trait]
    impl AsyncRunner for AsyncSocket1 {
        async fn one_iteration(&mut self) {
            self.stream.write(&self.buf).expect("writing socket");
            self.notify2.notify_one();

            self.notify1.notified().await;
            self.stream.read(&mut self.buf).expect("reading socket");
        }
        async fn on_cancelled(&mut self) {
            // discard any error
            let _ = self.stream.write(&self.buf);
            self.notify2.notify_one();
            self.cancel_barrier.wait().await;
        }
    }

    struct AsyncSocket2 {
        stream: UnixStream,
        buf: [u8; 1],
        notify1: Arc<tokio::sync::Notify>,
        notify2: Arc<tokio::sync::Notify>,
        cancel_barrier: Arc<tokio::sync::Barrier>,
    }

    #[async_trait]
    impl AsyncRunner for AsyncSocket2 {
        async fn one_iteration(&mut self) {
            self.notify2.notified().await;
            self.stream.read(&mut self.buf).expect("reading socket");

            self.stream.write(&self.buf).expect("writing socket");
            self.notify1.notify_one();
        }
        async fn on_cancelled(&mut self) {
            // discard any error
            let _ = self.stream.write(&self.buf);
            self.notify1.notify_one();
            self.cancel_barrier.wait().await;
        }
    }

    pub fn run_socket_async(num_pairs: u16, term: std::sync::Arc<AtomicBool>) -> u64 {
        let mut runners: Vec<Box<dyn AsyncRunner>> = Vec::with_capacity((num_pairs as usize) * 2);
        for _ in 0..num_pairs {
            let n1 = Arc::new(tokio::sync::Notify::new());
            let n2 = Arc::new(tokio::sync::Notify::new());
            let b = Arc::new(tokio::sync::Barrier::new(2));
            let pair = UnixStream::pair().expect("creating unix socket pair");
            runners.push(Box::new(AsyncSocket1 {
                stream: pair.0,
                buf: [0],
                notify1: n1.clone(),
                notify2: n2.clone(),
                cancel_barrier: b.clone(),
            }));
            runners.push(Box::new(AsyncSocket2 {
                stream: pair.1,
                buf: [0],
                notify1: n1.clone(),
                notify2: n2.clone(),
                cancel_barrier: b.clone(),
            }));
        }
        run_async(term, runners)
    }

    #[cfg(test)]
    mod tests {
        use std::{
            sync::atomic::Ordering,
            sync::{atomic::AtomicBool, Arc},
            time::Duration,
        };

        use crate::imp::run_socket_async;

        use super::run_futex;

        fn term_after(duration: Duration) -> Arc<AtomicBool> {
            let term = Arc::new(AtomicBool::new(false));
            let thread_term = term.clone();
            std::thread::spawn(move || {
                std::thread::sleep(duration);
                thread_term.store(true, Ordering::Release)
            });
            return term;
        }

        #[test]
        fn test_futex() {
            let term = term_after(Duration::from_secs(1));
            let num_echoes = run_futex(100, &term);
            assert!(num_echoes > 0);
        }

        #[test]
        fn test_socket_async() {
            let term = term_after(Duration::from_secs(1));
            let num_echoes = run_socket_async(100, term);
            assert!(num_echoes > 0);
        }
    }
}
