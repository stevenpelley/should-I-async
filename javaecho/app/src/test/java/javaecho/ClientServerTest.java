package javaecho;

import java.io.IOException;
import java.net.UnixDomainSocketAddress;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Executors;
import java.util.concurrent.StructuredTaskScope;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

import org.junit.jupiter.api.Test;

public class ClientServerTest {
    @Test
    void testClientServer()
            throws InterruptedException, ExecutionException, TimeoutException, IOException {
        var executor = Executors.newFixedThreadPool(2);
        String socketFileName = "/tmp/should-I-async_javaecho_ClientServerTest";
        Path path = Path.of(socketFileName);
        var address = UnixDomainSocketAddress.of(path);
        CompletableFuture<Void> serverCompletesAfterBind = new CompletableFuture<>();
        try {
            var serverFuture = executor.submit(() -> {
                var server = new Server(address, new Server.Injection(serverCompletesAfterBind));
                try {
                    server.listen();
                } catch (Exception ex) {
                    serverCompletesAfterBind
                            .completeExceptionally(new Exception("server exception", ex));
                    throw new RuntimeException(ex);
                }
            });

            try {
                serverCompletesAfterBind.get(5, TimeUnit.SECONDS);
            } catch (Exception ex) {
                throw ex;
            }

            var client = new Client("client1", address);
            client.roundTrip();
            serverFuture.get(5, TimeUnit.SECONDS);
        } finally {
            Files.deleteIfExists(path);
        }
    }

    @Test
    void testPreview() {
        try (var scope = new StructuredTaskScope.ShutdownOnFailure()) {
        }
    }
}
