package javaecho;

import java.util.Optional;
import java.util.concurrent.CompletableFuture;

public class StopConditions {
    final Optional<Long> iterations;
    final Optional<CompletableFuture<Void>> stop;

    StopConditions(Optional<Long> iterations, Optional<CompletableFuture<Void>> stop) {
        this.iterations = iterations;
        this.stop = stop;
    }

    public static StopConditions StopOnIterations(long iterations) {
        return new StopConditions(Optional.of(iterations), Optional.empty());
    }

    public static StopConditions StopOnFuture(CompletableFuture<Void> stop) {
        return new StopConditions(Optional.empty(), Optional.of(stop));
    }
}
