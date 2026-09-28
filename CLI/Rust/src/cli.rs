use std::env;

#[derive(Debug)]
pub enum Command {
    // Service
    Start,
    Stop,
    Restart,
    Status,

    // System
    Check(Option<String>),
    Doctor,
    Config,

    // Devices
    Devices,
    Ping(String),
    Info(Option<String>),

    // Workloads
    Run(Vec<String>),
    Jobs,
    Logs(Vec<String>),

    // General
    Version,
    Help,
}

pub fn parse_command() -> Command {
    let mut args = env::args().skip(1);

    let command = match args.next() {
        Some(command) => command.to_lowercase(),
        None => return Command::Help,
    };

    match command.as_str() {
        // -------------------------------------------------
        // Service
        // -------------------------------------------------

        "start" => Command::Start,

        "stop" => Command::Stop,

        "restart" | "reload" => Command::Restart,

        "status" | "state" => Command::Status,

        // -------------------------------------------------
        // System
        // -------------------------------------------------

        "check" => {
            Command::Check(args.next())
        }

        "doctor" | "diagnose" | "diagnostics" => {
            Command::Doctor
        }

        "config" | "configuration" => {
            Command::Config
        }

        // -------------------------------------------------
        // Devices
        // -------------------------------------------------

        "devices" | "nodes" => {
            Command::Devices
        }

        "ping" => {
            match args.next() {
                Some(device) => Command::Ping(device),

                None => {
                    eprintln!("Error: 'ping' requires a device.");
                    eprintln!("Usage: nodren ping <device>");
                    Command::Help
                }
            }
        }

        "info" => {
            Command::Info(args.next())
        }

        // -------------------------------------------------
        // Workloads
        // -------------------------------------------------

        "run" => {
            Command::Run(args.collect())
        }

        "jobs" | "job" => {
            Command::Jobs
        }

        "logs" | "log" => {
            Command::Logs(args.collect())
        }

        // -------------------------------------------------
        // General
        // -------------------------------------------------

        "version" | "--version" | "-v" => {
            Command::Version
        }

        "help" | "--help" | "-h" => {
            Command::Help
        }

        // -------------------------------------------------
        // Unknown
        // -------------------------------------------------

        unknown => {
            eprintln!("Unknown command: {unknown}");
            eprintln!("Run 'nodren help' for available commands.");

            Command::Help
        }
    }
}