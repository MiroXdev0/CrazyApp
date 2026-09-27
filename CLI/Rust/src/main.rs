mod cli;
mod commands;
mod system;

fn main() {
    let command = cli::parse_command();

    commands::execute(command);
}