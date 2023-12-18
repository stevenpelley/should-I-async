package javaecho;

import java.io.IOException;
import java.net.SocketAddress;
import java.nio.ByteBuffer;
import java.nio.channels.SocketChannel;

public class Client {
    final String name;
    final SocketAddress address;
    final ByteBuffer buf;
    final StopConditions stopConditions;

    public Client(String name, SocketAddress address, StopConditions stopConditions) {
        this.name = name;
        this.address = address;
        this.buf = ByteBuffer.allocate(100);
        this.stopConditions = stopConditions;
    }

    public void roundTripLoop() throws IOException {
        try (SocketChannel sc = SocketChannel.open(this.address)) {
            Common.roundTripLoop(sc, this.stopConditions, this.name.getBytes(), this.buf);
        }
    }
}
