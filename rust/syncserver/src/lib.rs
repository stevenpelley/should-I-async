use std::net::{SocketAddr, TcpListener, TcpStream};
use std::io::{BufReader, Read, Write, Error, BufWriter};
use std::thread;

const MAX_MESSAGE_SIZE: usize = 128;
const NUM_CLIENTS: i32 = 10;

/// Trait defining behavior for the server/client
pub trait Runnable {

    /// Start the server/client. The implementation of this function is expected to loop infinitely
    fn start(&self) -> Result<(), Error>;

    /// Perform one send/receive cycle with the other end of the socket.
    /// Return the TcpStream if it's still open, create an error otherwise.
    fn handle_connection(thread_id: i32, stream: TcpStream) -> Result<(), Error>;
}

// TcpServer: Start the listener and delegate incoming connections to other threads.

pub struct TcpServer {
    pub socket: SocketAddr
}

// Runnable handler for the TCP server
impl Runnable for TcpServer {
    fn start(&self) -> Result<(), Error> {
        let listener = TcpListener::bind(self.socket)?;
        let mut thread_id = 0;
        for stream in listener.incoming() {
            let stream = stream.unwrap();
            thread::spawn(move || TcpServer::handle_connection(thread_id, stream));
            thread_id += 1;
        }
        Ok(())
    }

    fn handle_connection(_thread_id: i32, mut stream: TcpStream) -> Result<(),Error> {
        loop {
            let mut buf: [u8; MAX_MESSAGE_SIZE] = [0; MAX_MESSAGE_SIZE];
            let offset = BufReader::new(&mut stream).read(&mut buf)?;
            let _ = BufWriter::new(&mut stream).write(&buf[0..offset]);
        }
    }
}

// TcpClient. Will launch NUM_CLIENTS threads each talking to the client.

pub struct TcpClient {
    pub socket: SocketAddr
}

impl Runnable for TcpClient {

    /// Launch NUM_CLIENTS each running on their own thread and interacting with the server
    fn start(&self) -> Result<(), Error> {
        let mut handles = Vec::with_capacity(NUM_CLIENTS as usize);

        for thread_id in 0..NUM_CLIENTS {
            let stream = TcpStream::connect(self.socket)?;
            handles.push(thread::spawn(move || TcpClient::handle_connection(thread_id, stream)));
        }

        for handle in handles {
            let _ = handle.join().unwrap();
        }
        Ok(())
    }

    /// Talk back and forth between the client and the server. The client will initiate a payload
    /// send it during each loop
    fn handle_connection(thread_id: i32, mut stream: TcpStream) -> Result<(), Error> {
        loop {
            let payload_str = format!("thread/{}", thread_id);
            let payload = payload_str.as_bytes();
            let expected_offset = BufWriter::new(&mut stream).write(payload)?;

            let mut buf: [u8; MAX_MESSAGE_SIZE] = [0; MAX_MESSAGE_SIZE];
            let actual_offset = BufReader::new(&mut stream).read(&mut buf)?;

            assert_eq!(expected_offset, actual_offset);
            println!("interacted on thread/{}", thread_id);
        }
    }
}