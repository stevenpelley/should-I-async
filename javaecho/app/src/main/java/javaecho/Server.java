package javaecho;

import java.io.IOException;
import java.net.SocketAddress;
import java.net.StandardProtocolFamily;
import java.nio.ByteBuffer;
import java.nio.channels.ClosedChannelException;
import java.nio.channels.ServerSocketChannel;
import java.nio.channels.SocketChannel;
import java.time.Duration;
import java.time.Instant;
import java.util.Arrays;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.StructuredTaskScope;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.TimeoutException;
import com.google.common.base.Preconditions;

public class Server implements AutoCloseable {
    final SocketAddress address;
    final Injection injection;
    final StopConditions stopConditions;
    final Duration gracefulShutdownDuration;
    final ConnectionMetricsSet connectionMetricsSet;
    // set as soon as it is created so that this server can be closed from the
    // outside.
    ServerSocketChannel serverSocketChannel;

    public static class Injection {
        final CompletableFuture<Void> completeAfterBind;

        public Injection(CompletableFuture<Void> completeAfterBind) {
            this.completeAfterBind = completeAfterBind;
        }
    }

    public Server(SocketAddress address, Injection injection, StopConditions stopConditions,
            Duration gracefulShutdownDuration, ConnectionMetricsSet connectionMetricsSet) {
        this.address = Preconditions.checkNotNull(address, "Server: null address");
        this.injection = Preconditions.checkNotNull(injection, "Server: null injection");
        this.stopConditions =
                Preconditions.checkNotNull(stopConditions, "Server: null stopConditions");
        this.gracefulShutdownDuration = Preconditions.checkNotNull(gracefulShutdownDuration,
                "Server: null gracefulShutdownDuration");
        this.connectionMetricsSet = connectionMetricsSet;
        this.serverSocketChannel = null;
    }

    /**
     * Listens on the provided socket address, handling each connected socket. Blocks indefinitely
     * until the calling thread is interrupted, at which point it will block until all
     * connection-handling threads return.
     * 
     * @throws IOException
     * @throws TimeoutException
     * @throws InterruptedException
     */
    public void listenAndHandle(ThreadFactory threadFactory)
            throws IOException, InterruptedException, TimeoutException {
        this.serverSocketChannel = ServerSocketChannel.open(StandardProtocolFamily.UNIX);
        this.serverSocketChannel.bind(this.address);
        this.injection.completeAfterBind.complete(null);

        try (StructuredTaskScope<Void> scope =
                new StructuredTaskScope<>("ServerHandling", threadFactory)) {
            while (true) {
                SocketChannel sc;
                try {
                    sc = this.serverSocketChannel.accept();
                } catch (ClosedChannelException ex) {
                    // we have stopped accepting connections and will give some time to exit
                    // gracefully.
                    break;
                }
                scope.fork(() -> {
                    handle(sc);
                    return null;
                });
            }

            scope.joinUntil(Instant.now().plus(this.gracefulShutdownDuration));
        }
    }

    void handle(SocketChannel socketChannel) throws IOException {
        try (SocketChannel sc = socketChannel) {
            ByteBuffer buf = ByteBuffer.allocate(100);
            Common.read(sc, buf, null);
            byte[] bytes = Arrays.copyOf(buf.array(), buf.position());

            var isEOF = Common.roundTripLoop(sc, this.stopConditions, bytes, buf,
                    this.connectionMetricsSet.newConnection());
            Preconditions.checkState(isEOF, "Server::handle: server should have received EOF");
        }
    }

    public void close() throws IOException {
        if (this.serverSocketChannel == null) {
            throw new IllegalStateException("Must call listenAndHandle prior to closing Server");
        }
        this.serverSocketChannel.close();
    }
}
