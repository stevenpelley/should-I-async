package javaecho;

import java.io.IOException;
import java.net.SocketAddress;
import java.nio.ByteBuffer;
import java.nio.channels.SocketChannel;
import java.util.Arrays;

import com.google.common.base.Preconditions;

public class Client {
    String name;
    SocketAddress address;

    public Client(String name, SocketAddress address) {
        this.name = name;
        this.address = address;
    }

    public void roundTrip() throws IOException {
        ByteBuffer buf = ByteBuffer.allocate(100);
        byte[] nameBytes = this.name.getBytes();
        try (SocketChannel sc = SocketChannel.open(address)) {
            var isEOF = roundTripX(sc, buf, nameBytes);
            Preconditions.checkState(!isEOF);
        }
    }

    /**
     * @param sc
     * @param buf
     * @param expectedBytes
     * @return true if EOF reached, false otherwise
     * @throws IOException
     */
    static boolean read(SocketChannel sc, ByteBuffer buf, byte[] expectedBytes) throws IOException {
        buf.clear();
        int read = sc.read(buf);
        if (read == -1) {
            return true;
        }
        if (expectedBytes != null) {
            Preconditions.checkState(Arrays.equals(expectedBytes, 0, expectedBytes.length,
                    buf.array(), 0, expectedBytes.length));
            Preconditions.checkState(read == expectedBytes.length);
        }
        return false;
    }

    static void write(SocketChannel sc, ByteBuffer buf, byte[] bytes) throws IOException {
        buf.clear();
        buf.put(bytes);
        buf.flip();
        int written = sc.write(buf);
        Preconditions.checkState(written == bytes.length);
        Preconditions.checkState(!buf.hasRemaining());
    }

    /**
     * @param sc
     * @param buf
     * @param bytes
     * @return true if EOF reached, false otherwise
     * @throws IOException
     */
    static boolean roundTripX(SocketChannel sc, ByteBuffer buf, byte[] bytes) throws IOException {
        write(sc, buf, bytes);
        return read(sc, buf, bytes);
    }
}
