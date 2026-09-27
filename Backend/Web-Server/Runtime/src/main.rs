use std::io::{BufRead, BufReader, Write};
use std::net::{TcpListener, TcpStream};

const RUNTIME_ADDR: &str = "127.0.0.1:9100";
const MAIN_SERVER_ADDR: &str = "http://127.0.0.1:8080";

fn main() {
    println!("[Nodren Runtime] starting...");
    println!("[Nodren Runtime] listening on {RUNTIME_ADDR}");
    println!("[Nodren Runtime] main server: {MAIN_SERVER_ADDR}");

    let listener =
        TcpListener::bind(RUNTIME_ADDR).expect("failed to bind Nodren Runtime");

    for stream in listener.incoming() {
        match stream {
            Ok(stream) => {
                println!("[Nodren Runtime] Gateway connected");

                if let Err(error) = handle_gateway(stream) {
                    eprintln!("[Nodren Runtime] connection error: {error}");
                }
            }

            Err(error) => {
                eprintln!("[Nodren Runtime] failed connection: {error}");
            }
        }
    }
}

fn handle_gateway(mut stream: TcpStream) -> std::io::Result<()> {
    let reader_stream = stream.try_clone()?;
    let mut reader = BufReader::new(reader_stream);

    let mut request = String::new();

    loop {
        request.clear();

        let bytes = reader.read_line(&mut request)?;

        if bytes == 0 {
            println!("[Nodren Runtime] Gateway disconnected");
            break;
        }

        let request = request.trim();

        if request.is_empty() {
            continue;
        }

        println!("[Nodren Runtime] request: {request}");

        let response = handle_request(request);

        stream.write_all(response.as_bytes())?;
        stream.write_all(b"\n")?;
        stream.flush()?;
    }

    Ok(())
}

fn handle_request(request: &str) -> String {
    match request {
        "PING" => "PONG".to_string(),

        "STATUS" => {
            match main_server_request("/health") {
                Ok(body) => format!("RUNTIME_ONLINE|MAIN_SERVER:{body}"),

                Err(error) => {
                    eprintln!(
                        "[Nodren Runtime] main server status error: {error}"
                    );

                    "RUNTIME_ONLINE|MAIN_SERVER_OFFLINE".to_string()
                }
            }
        }

        "NODES" => {
            match main_server_request("/v1/nodes") {
                Ok(body) => body,

                Err(error) => {
                    eprintln!(
                        "[Nodren Runtime] nodes request error: {error}"
                    );

                    r#"{"error":"main server unavailable"}"#.to_string()
                }
            }
        }

        "JOBS" => {
            match main_server_request("/v1/jobs") {
                Ok(body) => body,

                Err(error) => {
                    eprintln!(
                        "[Nodren Runtime] jobs request error: {error}"
                    );

                    r#"{"error":"main server unavailable"}"#.to_string()
                }
            }
        }

        _ => "UNKNOWN_REQUEST".to_string(),
    }
}

fn main_server_request(path: &str) -> Result<String, String> {
    let url = format!("{MAIN_SERVER_ADDR}{path}");

    let output = std::process::Command::new("curl")
        .args([
            "--silent",
            "--show-error",
            "--fail",
            "--max-time",
            "5",
            &url,
        ])
        .output()
        .map_err(|error| format!("failed to execute curl: {error}"))?;

    if !output.status.success() {
        return Err(format!(
            "curl exited with status {}",
            output.status
        ));
    }

    String::from_utf8(output.stdout)
        .map_err(|error| format!("invalid UTF-8 response: {error}"))
}
