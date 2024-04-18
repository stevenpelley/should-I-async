mod exec {
    #[cfg(test)]
    mod hello_world_poll_test {
        use std::{
            future::Future,
            ptr::null,
            task::{Context, RawWaker, RawWakerVTable, Waker},
        };

        // "noop_waker" is a future that always completes and never returns pending.
        // the associated raw waker has no state and is merely a null pointer

        static NOOP_RAW_WAKER_VTABLE: RawWakerVTable = RawWakerVTable::new(
            |_: *const ()| RawWaker::new(null(), &NOOP_RAW_WAKER_VTABLE),
            |_: *const ()| {},
            |_: *const ()| {},
            |_: *const ()| {},
        );

        #[test]
        fn test_noop_waker() {
            let waker = unsafe { Waker::from_raw(RawWaker::new(null(), &NOOP_RAW_WAKER_VTABLE)) };
            let mut ctx = Context::from_waker(&waker);
            let mut f = Box::pin(async { 0 as usize });
            let poll = f.as_mut().poll(&mut ctx);
            match poll {
                std::task::Poll::Ready(val) => assert_eq!(0, val),
                std::task::Poll::Pending => panic!("future still pending"),
            }
        }

        // CountdownFuture is a future that returns pending countdown times.
        struct CountdownFuture {
            countdown: usize,
        }

        impl Future for CountdownFuture {
            type Output = ();

            fn poll(
                self: std::pin::Pin<&mut Self>,
                _ctx: &mut Context<'_>,
            ) -> std::task::Poll<Self::Output> {
                if self.countdown == 0 {
                    return std::task::Poll::Ready(());
                } else {
                    self.get_mut().countdown -= 1;
                    return std::task::Poll::Pending;
                }
            }
        }

        #[test]
        fn test_countdown_future() {
            let waker = unsafe { Waker::from_raw(RawWaker::new(null(), &NOOP_RAW_WAKER_VTABLE)) };
            let mut ctx = Context::from_waker(&waker);
            let countdown = 5;
            let mut f = Box::pin(async {
                let cdf = CountdownFuture { countdown };
                cdf.await;
                0 as usize
            });
            let mut count = 0;
            let future_val;
            loop {
                count += 1;
                let poll = f.as_mut().poll(&mut ctx);
                match poll {
                    std::task::Poll::Ready(val) => {
                        future_val = val;
                        break;
                    }
                    std::task::Poll::Pending => {}
                }
            }
            assert_eq!(0, future_val);
            assert_eq!(countdown + 1, count);
        }
    }

    #[cfg(test)]
    mod intset_waker_test {
        use std::sync::Mutex;
        use std::task::{Context, RawWaker, RawWakerVTable, Waker};

        use std::future::Future;

        use once_cell::sync::Lazy;

        // int waker tracks an int per future and adds this int to a set when awoken.
        static AWOKEN_INTS: Lazy<Mutex<std::collections::HashSet<u64>>> =
            Lazy::new(|| Mutex::new(std::collections::HashSet::new()));
        static PENDING_INTS: Lazy<Mutex<std::collections::HashMap<u64, Waker>>> =
            Lazy::new(|| Mutex::new(std::collections::HashMap::new()));

        fn unwrap_awoken(f: fn(&mut std::collections::HashSet<u64>) -> ()) {
            f(&mut AWOKEN_INTS.lock().unwrap());
        }

        fn unwrap_pending(f: fn(&mut std::collections::HashMap<u64, Waker>) -> ()) {
            f(&mut PENDING_INTS.lock().unwrap());
        }

        // the raw waker points to u64
        static INT_RAW_WAKER_VTABLE: RawWakerVTable = RawWakerVTable::new(
            |data: *const ()| {
                return RawWaker::new(data, &INT_RAW_WAKER_VTABLE);
            },
            |data: *const ()| {
                unsafe { AWOKEN_INTS.lock().unwrap().insert(*(data as *const u64)) };
            },
            |data: *const ()| {
                unsafe { AWOKEN_INTS.lock().unwrap().insert(*(data as *const u64)) };
            },
            |_: *const ()| {},
        );

        // CountdownFuture is a future that returns pending countdown times.
        struct PauseFuture {
            has_paused: bool,
            value: u64,
        }

        impl Future for PauseFuture {
            type Output = ();

            fn poll(
                mut self: std::pin::Pin<&mut Self>,
                ctx: &mut Context<'_>,
            ) -> std::task::Poll<Self::Output> {
                if self.has_paused {
                    return std::task::Poll::Ready(());
                } else {
                    self.has_paused = true;
                    PENDING_INTS
                        .lock()
                        .unwrap()
                        .insert(self.value, ctx.waker().clone());
                    return std::task::Poll::Pending;
                }
            }
        }

        #[test]
        fn test_int_waker() {
            let waker_id = 1 as u64;
            let waker = unsafe {
                Waker::from_raw(RawWaker::new(
                    (&waker_id as *const u64) as *const (),
                    &INT_RAW_WAKER_VTABLE,
                ))
            };
            let mut ctx = Context::from_waker(&waker);
            let mut f = Box::pin(async {
                let inner_f = PauseFuture {
                    has_paused: false,
                    value: 1,
                };
                inner_f.await;
                0 as usize
            });

            unwrap_pending(|p| assert!(p.is_empty()));
            unwrap_awoken(|a| assert!(a.is_empty()));

            let poll = f.as_mut().poll(&mut ctx);
            assert_eq!(std::task::Poll::Pending, poll);
            unwrap_pending(|p| {
                assert_eq!(1, p.len());
                assert!(p.contains_key(&1));
            });
            unwrap_awoken(|a| assert!(a.is_empty()));

            // wake
            unwrap_pending(|p| p.get(&1).expect("key 1 missing").wake_by_ref());
            unwrap_awoken(|a| {
                assert_eq!(1, a.len());
                assert!(a.contains(&1));
            });

            let poll = f.as_mut().poll(&mut ctx);
            assert_eq!(std::task::Poll::Ready(0), poll);
        }
    }
}
