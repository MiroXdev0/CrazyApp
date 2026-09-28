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
    parse_from(env::args().skip(1))
}

pub fn parse_from<I, S>(args: I) -> Command
where
    I: IntoIterator<Item = S>,
    S: Into<String>,
{
    let mut args = args.into_iter().map(Into::into);

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
        "check" => Command::Check(args.next()),

        "doctor" | "diagnose" | "diagnostics" => Command::Doctor,

        "config" | "configuration" => Command::Config,

        // -------------------------------------------------
        // Devices
        // -------------------------------------------------
        "devices" | "nodes" => Command::Devices,

        "ping" => match args.next() {
            Some(device) => Command::Ping(device),

            None => {
                eprintln!("Error: 'ping' requires a device.");
                eprintln!("Usage: nodren ping <device>");
                Command::Help
            }
        },

        "info" => Command::Info(args.next()),

        // -------------------------------------------------
        // Workloads
        // -------------------------------------------------
        "run" => Command::Run(args.collect()),

        "jobs" | "job" => Command::Jobs,

        "logs" | "log" => Command::Logs(args.collect()),

        // -------------------------------------------------
        // General
        // -------------------------------------------------
        "version" | "--version" | "-v" => Command::Version,

        "help" | "--help" | "-h" => Command::Help,

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

#[cfg(test)]
mod tests {
    use super::{Command, parse_from};

    #[test]
    fn parses_controller_commands() {
        assert!(matches!(parse_from(["status"]), Command::Status));
        assert!(matches!(parse_from(["devices"]), Command::Devices));
        assert!(
            matches!(parse_from(["info", "worker-a"]), Command::Info(Some(worker)) if worker == "worker-a")
        );
    }

    #[test]
    fn parses_workload_arguments_without_interpreting_them() {
        assert!(matches!(
            parse_from(["run", "dot_product", "1,2", "3,4"]),
            Command::Run(arguments) if arguments == ["dot_product", "1,2", "3,4"]
        ));
    }

    #[test]
    fn parses_aliases_and_help() {
        assert!(matches!(parse_from(["state"]), Command::Status));
        assert!(matches!(parse_from(["--version"]), Command::Version));
        assert!(matches!(
            parse_from(std::iter::empty::<String>()),
            Command::Help
        ));
    }
}
