package javaecho;

import java.io.IOException;
import java.net.SocketAddress;
import java.net.SocketException;
import java.nio.ByteBuffer;
import java.nio.channels.SocketChannel;
import java.time.Duration;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Semaphore;
import java.util.concurrent.StructuredTaskScope;
import java.util.concurrent.ThreadFactory;

public class Client {
    final String name;
    final SocketAddress address;
    final ByteBuffer buf;
    final StopConditions stopConditions;
    final ConnectionMetricsSet.ConnectionMetrics connectionMetrics;
    final ConnectArgs connectArgs;

    public static class ConnectArgs {
        final Semaphore connectSemaphore;
        final int maxRetries;
        final Duration retrySleep;

        public ConnectArgs(Semaphore connSemaphore, int maxRetries, Duration retrySleep) {
            this.connectSemaphore = connSemaphore;
            this.maxRetries = maxRetries;
            this.retrySleep = retrySleep;
        }
    }

    public Client(String name, SocketAddress address, StopConditions stopConditions,
            ConnectionMetricsSet connectionMetricsSet, ConnectArgs connectArgs) {
        this.name = name;
        this.address = address;
        this.buf = ByteBuffer.allocate(100);
        this.stopConditions = stopConditions;
        this.connectionMetrics = connectionMetricsSet.newConnection();
        this.connectArgs = connectArgs;
    }

    class ConnectRetriesExhaustedException extends Exception {
        public ConnectRetriesExhaustedException() {
            super();
        }

        public ConnectRetriesExhaustedException(Throwable cause) {
            super(cause);
        }
    }

    SocketChannel connect()
            throws IOException, InterruptedException, ConnectRetriesExhaustedException {
        this.connectArgs.connectSemaphore.acquire();
        try {
            Exception lastException = null;
            for (int i = 0; i < this.connectArgs.maxRetries; i++) {
                try {
                    return SocketChannel.open(this.address);
                } catch (SocketException ex) {
                    lastException = ex;
                    Thread.sleep(this.connectArgs.retrySleep);
                    continue;
                }
            }
            if (lastException != null) {
                throw new ConnectRetriesExhaustedException(lastException);
            }
            throw new ConnectRetriesExhaustedException();
        } finally {
            this.connectArgs.connectSemaphore.release();
        }
    }

    void roundTripLoop()
            throws IOException, InterruptedException, ConnectRetriesExhaustedException {
        try (SocketChannel sc = this.connect()) {
            Common.roundTripLoop(sc, this.stopConditions, this.name.getBytes(), this.buf,
                    this.connectionMetrics);
        }
    }

    /**
     * Runs the clients in a StructuredTaskScope on threads provided by threadFactory. This call
     * blocks until one of the clients throws an uncaught exception, all clients complete, or the
     * calling thread is interrupted (at which point it continues to block until all client threads
     * return).
     * 
     * @param numClients
     * @param threadFactory
     * @param address
     * @param stopConditions
     * @throws InterruptedException if calling thread is interrupted
     * @throws ExecutionException if any of the component client's fails with Exception
     */
    public static void runClients(int numClients, ThreadFactory threadFactory,
            SocketAddress address, StopConditions stopConditions,
            ConnectionMetricsSet connectionMetricsSet, ConnectArgs connectArgs)
            throws InterruptedException, ExecutionException {
        int width = (int) (Math.log10(numClients) + 1);
        try (var scope = new StructuredTaskScope.ShutdownOnFailure("Clients", threadFactory)) {
            for (int i = 0; i < numClients; i++) {
                final String clientName = String.format("%1$0" + width + "d", i);
                scope.fork(() -> {
                    var client = new Client(clientName, address, stopConditions,
                            connectionMetricsSet, connectArgs);
                    client.roundTripLoop();
                    return null;
                });
            }
            scope.join().throwIfFailed();;
        }
    }
}
