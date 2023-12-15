package javaecho;

import java.io.IOException;
import java.net.SocketAddress;
import java.net.StandardProtocolFamily;
import java.nio.ByteBuffer;
import java.nio.channels.ServerSocketChannel;
import java.nio.channels.SocketChannel;
import java.util.Arrays;
import java.util.concurrent.CompletableFuture;

import com.google.common.base.Preconditions;

public class Server {
    final SocketAddress address;
    final Injection injection;

    public static class Injection {
        final CompletableFuture<Void> completeAfterBind;

        public Injection(CompletableFuture<Void> completeAfterBind) {
            this.completeAfterBind = completeAfterBind;
        }
    }

    public Server(SocketAddress address, Injection injection) {
        this.address = address;
        this.injection = injection;
    }

    public void listen() throws IOException {
        ServerSocketChannel ssc = ServerSocketChannel.open(StandardProtocolFamily.UNIX);
        ssc.bind(this.address);
        this.injection.completeAfterBind.complete(null);
        SocketChannel sc = ssc.accept();
        handle(sc);
    }

    static void handle(SocketChannel socketChannel) throws IOException {
        try (SocketChannel sc = socketChannel) {
            ByteBuffer buf = ByteBuffer.allocate(100);
            Client.read(sc, buf, null);
            byte[] bytes = Arrays.copyOf(buf.array(), buf.position());

            var isEOF = Client.roundTripX(sc, buf, bytes);
            Preconditions.checkState(isEOF);
        }
    }
}
