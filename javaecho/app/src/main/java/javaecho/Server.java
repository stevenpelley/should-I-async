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
    final StopConditions stopConditions;

    public static class Injection {
        final CompletableFuture<Void> completeAfterBind;

        public Injection(CompletableFuture<Void> completeAfterBind) {
            this.completeAfterBind = completeAfterBind;
        }
    }

    public Server(SocketAddress address, Injection injection, StopConditions stopConditions) {
        this.address = Preconditions.checkNotNull(address, "Server: null address");
        this.injection = Preconditions.checkNotNull(injection, "Server: null injection");
        this.stopConditions =
                Preconditions.checkNotNull(stopConditions, "Server: null stopConditions");
    }

    public void listen() throws IOException {
        ServerSocketChannel ssc = ServerSocketChannel.open(StandardProtocolFamily.UNIX);
        ssc.bind(this.address);
        this.injection.completeAfterBind.complete(null);
        SocketChannel sc = ssc.accept();
        handle(sc);
    }

    void handle(SocketChannel socketChannel) throws IOException {
        try (SocketChannel sc = socketChannel) {
            ByteBuffer buf = ByteBuffer.allocate(100);
            Common.read(sc, buf, null);
            byte[] bytes = Arrays.copyOf(buf.array(), buf.position());

            var isEOF = Common.roundTripLoop(sc, this.stopConditions, bytes, buf);
            Preconditions.checkState(isEOF, "Server::handle: server should have received EOF");
        }
    }
}
