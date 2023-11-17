use std::{
    env,
    io::Error,
    net::SocketAddr
};
use std::io::ErrorKind::InvalidInput;
use syncserver::{TcpServer, TcpClient, Runnable};

fn main() -> Result<(), Error>{
    let args: Vec<String> = env::args().collect();
    let mode = args.get(1)
        .ok_or_else(|| Error::new(InvalidInput, "mode (0) must be included"))?;
    let addr = args.get(2)
        .ok_or_else(|| Error::new(InvalidInput, "addr (1) must be included"))?;
    let socket = addr.parse::<SocketAddr>()
        .map_err(|e| Error::new(InvalidInput, e))?;

    let result = match mode.as_str() {
        "server" => TcpServer::new(socket).run(),
        "client" => TcpClient::new(socket).run(),
        _ => panic!("{}", Error::new(InvalidInput, "mode (0) must be either server or client"))
    };

    result
}