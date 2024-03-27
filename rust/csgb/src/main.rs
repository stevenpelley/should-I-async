use core::panic;
use std::{
    io::{Read, Write},
    os::unix::net::UnixStream,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
};

use clap::{arg, Command};

fn cli() -> Command {
    Command::new("csgb")
        .about("Context Switches Go Brrr")
        .subcommand_required(true)
        .arg_required_else_help(true)
        .subcommand(
            Command::new("yield")
                .about("threads yield repeatedly")
                .arg(
                    arg!(<THREADS> "number of threads to run")
                        .value_parser(clap::value_parser!(u16).range(1..)),
                )
                .arg_required_else_help(true),
        )
        .subcommand(
            Command::new("sleep")
                .about("threads sleep repeatedly")
                .arg(
                    arg!(<THREADS> "number of threads to run")
                        .value_parser(clap::value_parser!(u16).range(1..)),
                )
                .arg(
                    arg!(<SLEEP_NS> "number of ns to sleep")
                        .value_parser(clap::value_parser!(u64).range(0..1_000_000_000)),
                )
                .arg_required_else_help(true),
        )
        .subcommand(
            Command::new("futex")
                .about("futex pairs echo back and forth")
                .arg(
                    arg!(<THREAD_PAIRS> "number of thread pairs to run")
                        .value_parser(clap::value_parser!(u16).range(1..)),
                )
                .arg_required_else_help(true),
        )
        .subcommand(
            Command::new("socket")
                .about("socket pairs echo back and forth")
                .arg(
                    arg!(<THREAD_PAIRS> "number of thread pairs to run")
                        .value_parser(clap::value_parser!(u16).range(1..)),
                )
                .arg_required_else_help(true),
        )
}

trait Runner {
    fn one_iteration(&mut self);
    fn on_cancelled(&mut self);
}

fn run<'a, F>(num_threads: u16, term: Arc<AtomicBool>, runner_generator: F) -> u64
where
    F: Fn(u16) -> Box<dyn Runner + std::marker::Send + 'a>,
{
    return std::thread::scope(|scope| {
        let mut handles: Vec<std::thread::ScopedJoinHandle<u64>> =
            Vec::with_capacity(num_threads as usize);
        for thread_id in 0..num_threads {
            let thread_term = Arc::clone(&term);
            let mut thread_runner = runner_generator(thread_id);
            handles.push(scope.spawn(move || {
                let mut count: u64 = 0;
                loop {
                    if thread_term.load(Ordering::Relaxed) {
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

fn run_yield(num_threads: u16, term: Arc<AtomicBool>) -> u64 {
    return run(num_threads, term, |_| {
        return Box::new(YieldRunner {});
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

fn run_sleep(num_threads: u16, sleep_ns: &u64, term: Arc<AtomicBool>) -> u64 {
    return run(num_threads, term, |_| {
        return Box::new(SleepRunner {
            thread_sleep_ns: *sleep_ns,
        });
    });
}

struct FutexRunner {
    futex1: std::sync::Arc<linux_futex::Futex<linux_futex::Private>>,
    futex2: std::sync::Arc<linux_futex::Futex<linux_futex::Private>>,
    is_first_in_pair: bool,
}
impl FutexRunner {
    fn wait_on_futex(futex: &linux_futex::Futex<linux_futex::Private>) {
        loop {
            match futex.wait(0) {
                Ok(_) => {
                    break;
                }
                Err(linux_futex::WaitError::WrongValue) => {
                    break;
                }
                Err(linux_futex::WaitError::Interrupted) => {
                    continue;
                }
            }
        }
    }
}
impl Runner for FutexRunner {
    fn one_iteration(self: &mut FutexRunner) {
        if self.is_first_in_pair {
            self.futex2.value.store(1, Ordering::Release);
            self.futex2.wake(1);

            FutexRunner::wait_on_futex(&self.futex1);
            self.futex1.value.store(0, Ordering::Release);
        } else {
            FutexRunner::wait_on_futex(&self.futex2);
            self.futex2.value.store(0, Ordering::Release);

            self.futex1.value.store(1, Ordering::Release);
            self.futex1.wake(1);
        }
    }
    fn on_cancelled(self: &mut FutexRunner) {
        // force the other to wake up no matter which we are.
        self.futex1.value.store(1, Ordering::Release);
        self.futex1.wake(1);
        self.futex2.value.store(1, Ordering::Release);
        self.futex2.wake(1);
    }
}

fn run_futex(num_pairs: u16, term: Arc<AtomicBool>) -> u64 {
    let num_threads = num_pairs * 2;
    let mut futexes: Vec<std::sync::Arc<linux_futex::Futex<linux_futex::Private>>> =
        Vec::with_capacity(num_threads as usize);
    for _ in 0..num_threads {
        let mut_futexes = &mut futexes;
        mut_futexes.push(std::sync::Arc::new(linux_futex::Futex::new(0)));
    }

    return run(num_threads, term, move |thread_id| {
        let is_first_in_pair = thread_id % 2 == 0;
        let base_idx = if is_first_in_pair {
            thread_id
        } else {
            thread_id - 1
        } as usize;
        // each thread needs its own references to the relevant futexes
        let futex1 = futexes[base_idx].clone();
        let futex2 = futexes[base_idx + 1].clone();
        return Box::new(FutexRunner {
            futex1: futex1,
            futex2: futex2,
            is_first_in_pair: is_first_in_pair,
        });
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

fn run_socket(num_pairs: u16, term: Arc<AtomicBool>) -> u64 {
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
        return Box::new(SocketRunner {
            socket: socket,
            is_first_in_pair: is_first_in_pair,
            buf: [0],
        });
    });
}

fn main() {
    // set up a SIGTERM flag
    let term = Arc::new(AtomicBool::new(false));
    signal_hook::flag::register(signal_hook::consts::signal::SIGTERM, Arc::clone(&term))
        .expect("failed to register SIGTERM handler");
    signal_hook::flag::register(signal_hook::consts::signal::SIGINT, Arc::clone(&term))
        .expect("failed to register SIGINT handler");

    let matches = cli().get_matches();
    match matches.subcommand() {
        Some(("yield", sub_m)) => {
            let num_threads = sub_m.get_one::<u16>("THREADS").unwrap();
            println!("yielding {} threads", num_threads);
            let total_yields = run_yield(*num_threads, Arc::clone(&term));
            println!("yielded {} times", total_yields);
        }
        Some(("sleep", sub_m)) => {
            let num_threads = sub_m.get_one::<u16>("THREADS").unwrap();
            let sleep_ns = sub_m.get_one::<u64>("SLEEP_NS").unwrap();
            println!("sleeping {} threads, {} ns", num_threads, sleep_ns);
            let total_sleeps = run_sleep(*num_threads, sleep_ns, Arc::clone(&term));
            println!("slept {} times", total_sleeps);
        }
        Some(("futex", sub_m)) => {
            let num_thread_pairs = sub_m.get_one::<u16>("THREAD_PAIRS").unwrap();
            println!("futex {} thread pairs", num_thread_pairs);
            let total_round_trips = run_futex(*num_thread_pairs, Arc::clone(&term));
            println!("futex round trip echoes: {}", total_round_trips);
        }
        Some(("socket", sub_m)) => {
            let num_thread_pairs = sub_m.get_one::<u16>("THREAD_PAIRS").unwrap();
            println!("socket {} thread pairs", num_thread_pairs);
            let total_round_trips = run_socket(*num_thread_pairs, Arc::clone(&term));
            println!("socket round trip echoes: {}", total_round_trips);
        }
        _ => {
            std::unreachable!("subcommand was required")
        } // Either no subcommand or one not tested for...
    }
}

#[cfg(test)]
mod tests {
    use std::{
        sync::atomic::Ordering,
        sync::{atomic::AtomicBool, Arc},
        time::Duration,
    };

    use crate::run_futex;

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
        let num_echoes = run_futex(100, term);
        assert!(num_echoes > 0);
    }
}
