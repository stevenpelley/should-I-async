package javaecho;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.channels.SocketChannel;
import java.time.Duration;
import java.util.Arrays;
import java.util.concurrent.CompletableFuture;
import com.google.common.base.Preconditions;

public class Common {
    public static boolean roundTripLoop(SocketChannel sc, StopConditions stopConditions,
            Duration sleepDuration, byte[] bytes, ByteBuffer buf,
            ConnectionMetricsSet.ConnectionMetrics connectionMetrics)
            throws IOException, InterruptedException {
        for (long i = 0;; i++) {
            final long iFinal = i;
            if (stopConditions.iterations.map(iters -> iFinal >= iters).orElse(false)) {
                return false;
            }

            if (stopConditions.stop.map(CompletableFuture::isDone).orElse(false)) {
                return false;
            }

            final var isEOF = roundTrip(sc, buf, bytes);
            stopConditions.iterations.ifPresent(iters -> Preconditions.checkState(!isEOF,
                    "EOF with iterations stop condition"));
            if (isEOF) {
                return true;
            }

            connectionMetrics.record();

            // insert sleep between previous read and subsequent write
            if (sleepDuration.isPositive()) {
                Thread.sleep(sleepDuration);
            }
        }
    }

    /**
     * @param sc
     * @param buf
     * @param bytes
     * @return true if EOF reached, false otherwise
     * @throws IOException
     */
    static boolean roundTrip(SocketChannel sc, ByteBuffer buf, byte[] bytes) throws IOException {
        write(sc, buf, bytes);
        return read(sc, buf, bytes);
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
            Preconditions.checkState(read == expectedBytes.length,
                    "Common::read: incorrect array length");
            Preconditions.checkState(Arrays.equals(expectedBytes, 0, expectedBytes.length,
                    buf.array(), 0, expectedBytes.length), "Common::read: incorrect bytes read");
        }
        return false;
    }

    static void write(SocketChannel sc, ByteBuffer buf, byte[] bytes) throws IOException {
        buf.clear();
        buf.put(bytes);
        buf.flip();
        int written = sc.write(buf);
        Preconditions.checkState(written == bytes.length, "Common::write: incorrect array length");
        Preconditions.checkState(!buf.hasRemaining(),
                "Common::write: buffer still has bytes remaining after write");
    }
}
