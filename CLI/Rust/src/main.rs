mod api;
mod cli;
mod commands;
mod system;

fn main() {
    let command = cli::parse_command();

    if let Err(error) = commands::execute(command) {
        eprintln!("[ERROR] {error}");
        std::process::exit(1);
    }
}
