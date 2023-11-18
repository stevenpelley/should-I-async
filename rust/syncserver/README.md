# TCP Server
Multithreaded server/client. The client will just send dummy data. The server will echo back. Launch the server before
the client (if not, you'll immediately encounter a refused connection error).

# Usage
## Start server:
```
cargo run server 127.0.0.1:8080
```

## Start clients:
```
cargo run client 127.0.0.1:8080
```