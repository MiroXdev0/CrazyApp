use crate::cli::Command;
use crate::system::{platform, processes};

pub fn execute(command: Command) {
    match command {
        Command::Start => start(),
        Command::Stop => stop(),
        Command::Status => status(),
        Command::Check(device) => check(device),
        Command::Devices => devices(),
        Command::Run(args) => run(args),
        Command::Version => version(),
        Command::Help => help(),
    }
}

fn start() {
    println!("Starting Nodren...");

    println!("Checking local environment...");
    println!("Platform: {}", platform::name());

    // Later:
    // - Start Controller
    // - Start Worker
    // - Start Web Runtime
    // - Start Gateway
}

fn stop() {
    println!("Stopping Nodren...");

    // Later:
    // - Find Nodren processes
    // - Shut them down cleanly
}

fn status() {
    println!("Nodren Status");
    println!("-------------");

    println!(
        "Platform: {}",
        platform::name()
    );

    println!(
        "Nodren processes: {}",
        processes::count_nodren_processes()
    );
}

fn check(device: Option<String>) {
    match device {
        Some(device) => {
            println!("Checking device: {device}");

            // Later this becomes a controller request:
            // GET /v1/nodes/{device}
        }

        None => {
            println!("Nodren System Check");
            println!("-------------------");

            println!("[OK] CLI");
            println!("[OK] Platform: {}", platform::name());

            let process_count = processes::count_nodren_processes();

            println!(
                "[INFO] Nodren processes detected: {process_count}"
            );
        }
    }
}

fn devices() {
    println!("Nodren Devices");
    println!("--------------");

    // Later this will request the device list
    // from the Go Controller.
}

fn run(args: Vec<String>) {
    if args.is_empty() {
        println!("Usage: Nodren run <workload>");
        return;
    }

    println!("Submitting workload:");

    for arg in args {
        println!("  {arg}");
    }

    // Later:
    // CLI -> Controller -> Scheduler -> Worker
}

fn version() {
    println!("Nodren CLI 0.1.0");
}

fn help() {
    println!(
        r#"Nodren CLI

Usage:
    Nodren <command>

Commands:
    start               Start Nodren
    stop                Stop Nodren
    status              Show system status
    check               Check local Nodren system
    check <device>      Check a specific device
    devices             List Nodren devices
    run <workload>      Submit a workload
    version             Show version
    help                Show this help

Examples:
    Nodren start
    Nodren status
    Nodren check
    Nodren check DESKTOP-01
    Nodren devices
    Nodren run simulation
"#
    );
}