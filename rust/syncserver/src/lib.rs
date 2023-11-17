use std::net::{SocketAddr, TcpListener, TcpStream};
use std::io::{BufReader, Read, Write, Error};
use std::thread;

const MAX_MESSAGE_SIZE: usize = 128;

pub trait Runnable {
    fn run(&self) -> Result<(),Error>;
}

pub struct TcpServer {
    listener: TcpListener
}

/// TcpServer that echoes the information back to the caller
impl TcpServer {
    pub fn new(socket: SocketAddr) -> TcpServer {
        // Create the TcpServer while listening on the socket
        TcpServer { listener: TcpListener::bind(socket).unwrap() }
    }
    fn handle(mut stream: TcpStream) -> Result<(),Error> {
        let mut line: [u8; MAX_MESSAGE_SIZE] = [0; MAX_MESSAGE_SIZE];
        BufReader::new(&mut stream).read(&mut line)?;
        stream.write(&mut line)?;
        Ok(())
    }
}

// Runnable handler for the TCP server
impl Runnable for TcpServer {
    fn run(&self) -> Result<(),Error> {
        for stream in self.listener.incoming() {
            thread::spawn(move || TcpServer::handle(stream.unwrap()));
        }
        Ok(())
    }
}

pub struct TcpClient {
    stream: TcpStream
}

impl TcpClient {
    pub fn new(socket: SocketAddr) -> TcpClient {
        TcpClient { stream: TcpStream::connect(socket).unwrap() }
    }

    fn send(stream: &mut TcpStream, id: i32) -> Result<(),Error> {
        loop {
            let package_str = format!("thread/{}", id);
            let package = package_str.as_bytes();
            match stream.write(package) {
                Ok(val) => { dbg!(val); }
                Err(_) => {
                    // skip for now
                }
            };
        }
    }
}

impl Runnable for TcpClient {
    fn run(&self) -> Result<(), Error> {
        let mut handles = Vec::with_capacity(10);
        for i in 0..10 {
            let mut child_stream = self.stream.try_clone()?;
            handles.push(thread::spawn(move || TcpClient::send(&mut child_stream, i)));
        }

        for handle in handles {
            let _ = handle.join().unwrap();
        }
        Ok(())
    }
}