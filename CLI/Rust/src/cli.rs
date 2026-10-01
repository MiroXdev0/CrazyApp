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
    Workers(WorkerCommand),
    JobControl(JobCommand),
    Tasks(TaskCommand),
    Distribution(DistributionCommand),
    Events,
    Logs(Vec<String>),

    // General
    Version,
    Help,
}

#[derive(Debug)]
pub enum WorkerCommand {
    List,
    Info(String),
    Ping(String),
    Pause(String),
    Resume(String),
    Remove(String),
    Stats(Option<String>),
}

#[derive(Debug)]
pub enum JobCommand {
    List,
    Info(String),
    Run(Vec<String>),
    Cancel(String),
    Pause(String),
    Resume(String),
    Stats(String),
    Partitions(String),
}

#[derive(Debug)]
pub enum TaskCommand {
    List,
    Info(String),
    Cancel(String),
    Retry(String),
    Logs(String),
    Result(String),
}

#[derive(Debug)]
pub enum DistributionCommand {
    Show(Option<String>),
    Auto(String),
    Set(String, Vec<String>),
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

        "workers" => parse_workers(args.collect()),

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

        "jobs" | "job" => parse_jobs(args.collect()),

        "tasks" | "task" => parse_tasks(args.collect()),

        "distribution" => parse_distribution(args.collect()),

        "logs" | "log" => Command::Logs(args.collect()),

        "events" | "monitor" => Command::Events,

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

fn parse_tasks(args: Vec<String>) -> Command {
    match args.as_slice() {
        [] => Command::Tasks(TaskCommand::List),
        [command] if command == "list" => Command::Tasks(TaskCommand::List),
        [command, id] if command == "info" => Command::Tasks(TaskCommand::Info(id.clone())),
        [command, id] if command == "cancel" => Command::Tasks(TaskCommand::Cancel(id.clone())),
        [command, id] if command == "retry" => Command::Tasks(TaskCommand::Retry(id.clone())),
        [command, id] if command == "logs" => Command::Tasks(TaskCommand::Logs(id.clone())),
        [command, id] if command == "result" => Command::Tasks(TaskCommand::Result(id.clone())),
        _ => Command::Help,
    }
}

fn parse_workers(args: Vec<String>) -> Command {
    match args.as_slice() {
        [] => Command::Workers(WorkerCommand::List),
        [command] if command == "list" => Command::Workers(WorkerCommand::List),
        [command] if command == "stats" => Command::Workers(WorkerCommand::Stats(None)),
        [command, id] if command == "info" => Command::Workers(WorkerCommand::Info(id.clone())),
        [command, id] if command == "ping" => Command::Workers(WorkerCommand::Ping(id.clone())),
        [command, id] if command == "pause" => Command::Workers(WorkerCommand::Pause(id.clone())),
        [command, id] if command == "resume" => Command::Workers(WorkerCommand::Resume(id.clone())),
        [command, id] if command == "remove" => Command::Workers(WorkerCommand::Remove(id.clone())),
        [command, id] if command == "stats" => {
            Command::Workers(WorkerCommand::Stats(Some(id.clone())))
        }
        _ => Command::Help,
    }
}

fn parse_jobs(args: Vec<String>) -> Command {
    match args.as_slice() {
        [] => Command::JobControl(JobCommand::List),
        [command] if command == "list" => Command::JobControl(JobCommand::List),
        [command, id] if command == "info" => Command::JobControl(JobCommand::Info(id.clone())),
        [command, id] if command == "stats" => Command::JobControl(JobCommand::Stats(id.clone())),
        [command, id] if command == "partitions" => {
            Command::JobControl(JobCommand::Partitions(id.clone()))
        }
        [command, id] if command == "cancel" => Command::JobControl(JobCommand::Cancel(id.clone())),
        [command, id] if command == "pause" => Command::JobControl(JobCommand::Pause(id.clone())),
        [command, id] if command == "resume" => Command::JobControl(JobCommand::Resume(id.clone())),
        [command, rest @ ..] if command == "run" => {
            Command::JobControl(JobCommand::Run(rest.to_vec()))
        }
        _ => Command::Help,
    }
}

fn parse_distribution(args: Vec<String>) -> Command {
    match args.as_slice() {
        [command] if command == "show" => Command::Distribution(DistributionCommand::Show(None)),
        [command, id] if command == "show" => {
            Command::Distribution(DistributionCommand::Show(Some(id.clone())))
        }
        [command, id] if command == "auto" => {
            Command::Distribution(DistributionCommand::Auto(id.clone()))
        }
        [command, id, allocations @ ..] if command == "set" => {
            Command::Distribution(DistributionCommand::Set(id.clone(), allocations.to_vec()))
        }
        _ => Command::Help,
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
