package javaecho;

import java.io.IOException;
import java.net.UnixDomainSocketAddress;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Executors;
import java.util.concurrent.Semaphore;
import java.util.concurrent.StructuredTaskScope;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.TimeoutException;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.Test;

public class ClientServerTest {
    static class TestConfig {
        int numClients = 512;
        long numIterations = 1000;
        Duration testTimeout = Duration.ofSeconds(30);
        Duration gracefulShutdownDuration = Duration.ofSeconds(2);
        int maxConnectConcurrency = 100;
        int maxConnectRetries = 5;
        Duration connectRetrySleep = Duration.ofMillis(1);
    }

    void clientServerHelper(ThreadFactory serverThreadFactory, ThreadFactory clientThreadFactory,
            TestConfig config)
            throws IOException, InterruptedException, TimeoutException, ExecutionException {
        // test state
        final String socketFileName = "/tmp/should-I-async_javaecho_ClientServerTest";
        final Path path = Path.of(socketFileName);
        Files.deleteIfExists(path);
        final var address = UnixDomainSocketAddress.of(path);
        final StopConditions neverStops = StopConditions.StopOnFuture(new CompletableFuture<>());
        final CompletableFuture<Void> serverCompletesAfterBind = new CompletableFuture<>();
        final ConnectionMetricsSet connectionMetricsSet = new ConnectionMetricsSet();
        final Client.ConnectArgs connectArgs =
                new Client.ConnectArgs(new Semaphore(config.maxConnectConcurrency),
                        config.maxConnectRetries, config.connectRetrySleep);

        // we expect the client subtask to finish first, which will
        // automatically shutdown the server. We'll verify afterwards that this was the case.
        try (final var scope = new StructuredTaskScope.ShutdownOnFailure("unit test",
                Executors.defaultThreadFactory());
                final var server = new Server(address,
                        new Server.Injection(serverCompletesAfterBind), neverStops,
                        config.gracefulShutdownDuration, connectionMetricsSet)) {
            scope.fork(() -> {
                Thread.currentThread().setName("server acceptor");
                server.listenAndHandle(serverThreadFactory);
                return null;
            });

            scope.fork(() -> {
                Thread.currentThread().setName("client driver");
                serverCompletesAfterBind.get();
                Client.runClients(config.numClients, clientThreadFactory, address,
                        StopConditions.StopOnIterations(config.numIterations), connectionMetricsSet,
                        connectArgs);
                // completed! Shut the server down
                server.close();
                return null;
            });

            scope.joinUntil(Instant.now().plus(config.testTimeout)).throwIfFailed();;
        } finally {
            Files.deleteIfExists(path);
        }

        // server gets one less complete round trip
        Assertions.assertEquals(
                (config.numClients * config.numIterations)
                        + (config.numClients * (config.numIterations - 1)),
                connectionMetricsSet.getStatistics().count);
    }

    @Test
    void testClientServerPlatformThreads()
            throws IOException, InterruptedException, TimeoutException, ExecutionException {
        var serverThreadFactory = Thread.ofPlatform().name("server").factory();
        var clientThreadFactory = Thread.ofPlatform().name("client").factory();
        clientServerHelper(serverThreadFactory, clientThreadFactory, new TestConfig());
    }

    @Test
    void testClientServerVirtualThreads()
            throws IOException, InterruptedException, TimeoutException, ExecutionException {
        var serverThreadFactory = Thread.ofVirtual().name("server").factory();
        var clientThreadFactory = Thread.ofVirtual().name("client").factory();
        var config = new TestConfig();
        clientServerHelper(serverThreadFactory, clientThreadFactory, config);
    }
}
