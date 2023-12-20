package javaecho;

import java.util.ArrayList;
import java.util.List;
import java.util.function.Supplier;
import java.util.stream.LongStream;

public class ConnectionMetricsSet {
    class ConnectionMetrics {
        public long count = 0;

        public void record() {
            this.count++;
        }

        long getCount() {
            return this.count;
        }
    }

    public class ConnectionStatistics {
        // to match go json
        //// Count int64 `json:"count"`
        //// MinCount int64 `json:"minCount"`
        //// MaxCount int64 `json:"maxCount"`
        //// AverageCount float64 `json:"averageCount"`
        //// StdDevCount float64 `json:"StdDevCount"`
        public final long count;
        public final long minCount;
        public final long maxCount;
        public final double averageCount;
        public final double StdDevCount;

        public ConnectionStatistics() {
            Supplier<LongStream> s = () -> ConnectionMetricsSet.this.list.stream()
                    .mapToLong(ConnectionMetrics::getCount);
            var summary = s.get().summaryStatistics();
            this.count = summary.getSum();
            this.minCount = summary.getMin();
            this.maxCount = summary.getMax();
            this.averageCount = summary.getAverage();

            // Variance
            double variance = s.get().mapToDouble(i -> i).map(i -> i - this.averageCount)
                    .map(i -> i * i).average().getAsDouble();

            // Standard Deviation
            this.StdDevCount = Math.sqrt(variance);
        }
    }

    List<ConnectionMetrics> list = new ArrayList<>();

    public synchronized ConnectionMetrics newConnection() {
        var cm = new ConnectionMetrics();
        list.add(cm);
        return cm;
    }

    /**
     * Return all metrics as a list. This method is unsynchronized and it is the user's
     * responsibility to ensure that no new connections will be added nor any existing connection
     * metrics updated.
     * 
     * @return
     */
    public List<ConnectionMetrics> getAllMetrics() {
        return this.list;
    }

    public ConnectionStatistics getStatistics() {
        return new ConnectionStatistics();
    }
}
