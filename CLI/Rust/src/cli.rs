use std::env;

#[derive(Debug)]
pub enum Command {
    Start,
    Stop,
    Status,
    Check(Option<String>),
    Devices,
    Run(Vec<String>),
    Version,
    Help,
}

pub fn parse_command() -> Command {
    let mut args = env::args().skip(1);

    match args.next().as_deref() {
        Some("start") => Command::Start,
        Some("stop") => Command::Stop,
        Some("status") => Command::Status,

        Some("check") => {
            Command::Check(args.next())
        }

        Some("devices") => Command::Devices,

        Some("run") => {
            Command::Run(args.collect())
        }

        Some("version") | Some("--version") | Some("-v") => {
            Command::Version
        }

        Some("help") | Some("--help") | Some("-h") | None => {
            Command::Help
        }

        Some(command) => {
            eprintln!("Unknown command: {command}");
            Command::Help
        }
    }
}