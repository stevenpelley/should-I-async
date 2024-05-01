use csgb;

use std::sync::{atomic::AtomicBool, Arc};

use clap::{arg, ArgAction, Command};

enum ThreadArg {
    NumThreads,
    NumThreadPairs,
}

fn subcommand(name: &'static str, about: &'static str, thread_arg: ThreadArg) -> clap::Command {
    return Command::new(name)
        .about(about)
        .arg(match thread_arg {
            ThreadArg::NumThreads => arg!(<THREADS> "number of threads to run")
                .value_parser(clap::value_parser!(u16).range(1..)),
            ThreadArg::NumThreadPairs => arg!(<THREAD_PAIRS> "number of thread pairs to run")
                .value_parser(clap::value_parser!(u16).range(1..)),
        })
        .arg_required_else_help(true);
}

fn cli() -> Command {
    Command::new("csgb")
        .about("Context Switches Go Brrr")
        .subcommand_required(true)
        .arg(
            clap::Arg::new("sync")
                .short('s')
                .long("sync")
                .required(false)
                .action(ArgAction::SetTrue)
                // does not take a value
                .num_args(0),
        )
        .subcommand(subcommand(
            "yield",
            "threads yield repeatedly",
            ThreadArg::NumThreads,
        ))
        .subcommand(
            subcommand("sleep", "threads sleep repeatedly", ThreadArg::NumThreads).arg(
                arg!(<SLEEP_NS> "number of ns to sleep")
                    .value_parser(clap::value_parser!(u64).range(0..1_000_000_000)),
            ),
        )
        .subcommand(subcommand(
            "futex",
            "futex pairs echo back and forth",
            ThreadArg::NumThreadPairs,
        ))
        .subcommand(subcommand(
            "socket",
            "socket pairs echo back and forth",
            ThreadArg::NumThreadPairs,
        ))
}

fn set_up_cancel_signal() -> Arc<AtomicBool> {
    let term = Arc::new(AtomicBool::new(false));
    signal_hook::flag::register(signal_hook::consts::signal::SIGTERM, Arc::clone(&term))
        .expect("failed to register SIGTERM handler");
    signal_hook::flag::register(signal_hook::consts::signal::SIGINT, Arc::clone(&term))
        .expect("failed to register SIGINT handler");
    return term;
}

fn main() {
    let term = set_up_cancel_signal();
    let matches = cli().get_matches();
    let is_sync = matches.get_one::<bool>("sync").unwrap_or(&false);
    println!("sync: {}", is_sync);
    let total_iterations: u64 = match (matches.subcommand(), is_sync) {
        (Some(("socket", sub_m)), s) => {
            let num_thread_pairs = sub_m.get_one::<u16>("THREAD_PAIRS").unwrap();
            if *s {
                csgb::run_socket(*num_thread_pairs, &term)
            } else {
                csgb::run_socket_async(*num_thread_pairs, term)
            }
        }
        // other subcommands do not have an async implementation
        (_, false) => std::unimplemented!("subcommand does not support async"),
        (Some(("yield", sub_m)), true) => {
            let num_threads = sub_m.get_one::<u16>("THREADS").unwrap();
            csgb::run_yield(*num_threads, &term)
        }
        (Some(("sleep", sub_m)), true) => {
            let num_threads = sub_m.get_one::<u16>("THREADS").unwrap();
            let sleep_ns = sub_m.get_one::<u64>("SLEEP_NS").unwrap();
            csgb::run_sleep(*num_threads, sleep_ns, &term)
        }
        (Some(("futex", sub_m)), true) => {
            let num_thread_pairs = sub_m.get_one::<u16>("THREAD_PAIRS").unwrap();
            csgb::run_futex(*num_thread_pairs, &term)
        }
        _ => {
            std::unreachable!("subcommand was required");
        } // Either no subcommand or one not tested for...
    };
    println!("total iterations: {}", total_iterations);
}
