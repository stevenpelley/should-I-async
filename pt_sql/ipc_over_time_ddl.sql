-- define a view of buckets for calculating IPC.  This uses simple python string
-- formatting with no input validation

-- fill in the start and end time as well as the steps (in cycles)
-- define the buckets for our plot.  We'll use the time ranges to assign
-- instructions and cycles to buckets.
--
-- note that this can be turned into more of a rolling window by using
-- bucket_cycles that is larger than the "step".  Buckets will then overlap
DROP TABLE IF EXISTS ipc_buckets;
CREATE TABLE ipc_buckets AS
select
    gs.value as bucket_start_cyc, 
    gs.value + {bucket_length_cyc} as bucket_end_cyc,
    {bucket_tick_cyc} as bucket_cycles
from generate_series(
    (
        select max(end_cum_cyc)
        from timestamped_ranges
        where end_time < (select min(column1) from time_bounds) -- start time (ns)
    ),
    (
        select min(end_cum_cyc)
        from timestamped_ranges 
        where end_time > (select max(column1) from time_bounds) -- end time (ns)
    ),
    {bucket_tick_cyc} -- step (cycles)
    ) as gs
;


DROP TABLE if exists  ipc_over_time;

-- define time buckets
-- assign instructions to buckets using interpolation, conservative, and
-- optimistic strategies; see comments below in query
-- calculate IPC for each bucket and each strategy
CREATE TABLE ipc_over_time as
with joined_buckets_to_ranges as (
    select
    -- locate the start and end time ranges of each bucket.
    -- we filter out buckets whose start or end range contains any null, which
    -- can happen for buckets that begin or end before or after all the samples.
        b.*,
        t1.start_sample_id as start_bucket_start_sample_id,
        t1.start_time      as start_bucket_start_time,
        t1.start_cum_insns as start_bucket_start_cum_insns,
        t1.start_cum_cyc   as start_bucket_start_cum_cyc,
        t1.end_sample_id   as start_bucket_end_sample_id,
        t1.end_time        as start_bucket_end_time,
        t1.end_cum_insns   as start_bucket_end_cum_insns,
        t1.end_cum_cyc     as start_bucket_end_cum_cyc,
        t1.insn_count      as start_bucket_insn_count,
        t1.cyc_count       as start_bucket_cyc_count,
        t2.start_sample_id as end_bucket_start_sample_id,
        t2.start_time      as end_bucket_start_time,
        t2.start_cum_insns as end_bucket_start_cum_insns,
        t2.start_cum_cyc   as end_bucket_start_cum_cyc,
        t2.end_sample_id   as end_bucket_end_sample_id,
        t2.end_time        as end_bucket_end_time,
        t2.end_cum_insns   as end_bucket_end_cum_insns,
        t2.end_cum_cyc     as end_bucket_end_cum_cyc,
        t2.insn_count      as end_bucket_insn_count,
        t2.cyc_count       as end_bucket_cyc_count,
        t1.end_sample_id = t2.end_sample_id as is_start_end_same,
        t2.end_sample_row_number - t1.end_sample_row_number + 1 as bucket_spans_ranges
    from
        -- TODO: this join remains the bottleneck, even with indices on start_cum_cyc and end_cum_cyc
        ipc_buckets as b left outer join timestamped_ranges as t1
        ON b.bucket_start_cyc between (t1.start_cum_cyc + 1) and t1.end_cum_cyc
        left outer join timestamped_ranges as t2
        ON b.bucket_end_cyc between (t2.start_cum_cyc + 1) and t2.end_cum_cyc
    -- don't want to deal with buckets without an end point.
    WHERE
        start_bucket_start_sample_id is not null and
        start_bucket_end_sample_id is not null and
        end_bucket_start_sample_id is not null and
        end_bucket_end_sample_id is not null
), buckets_ranges_insns as (
    select
    -- calculate the number of instructions and cycles in the bucket for the following 3 cases:
    --   cycles = bucket size
    --   insns = start range insns +
    --           "interior" (entirely within bucket) insns +
    --           end range insns
    --           - double count if start range is end range
    --   start range is end range: start_bucket_start_sample_id = end_bucket_start_sample_id
    --   interior insns = iff(
    --           startt range is end range,
    --           0,
    --           end_bucket_start_cum_insns - start_bucket_end_cum_insns)
    --
    -- linear interpolation: assume instructions retire evenly throughout each time range.
    -- this produces an IPC per bucket that would combine to produce an accurate
    -- IPC at coarser time scales.  In other words, each instruction is accounted for once.
    --   start range insns = insns * (range end cyc - bucket start cyc) / (range end cyc - range start cyc)
    --   end range insns = insns * (bucket end cyc - range start cyc) / (range end cyc - range start cyc)
    --   double count: start range insns
    --
    -- "conservative" assume all instructions in the boundary time ranges retire outside
    -- each bucket.  This results in the lowest possible IPC, but would under-estimate IPC
    -- when considered across buckets.  Instructions in boundary time ranges are not accounted
    -- for in any bucket.
    --   start range insns = 0
    --   end range insns = 0
    --   double count: 0
    -- 
    -- "optimistic" assume all instructions in the boundary time ranges retire inside
    -- each bucket.  This results in the highest possible IPC, but over-estimates IPC
    -- when considered across buckets.  Instructions in boundary time ranges are double counted.
    --   start range insns = insns
    --   end range insns = insns
    --   double count: start range insns
        *,
        iif(
            is_start_end_same,
            0,
            end_bucket_start_cum_insns - start_bucket_end_cum_insns
        ) as interior_insns,
        iif(is_start_end_same, start_bucket_insn_count, 0) as linear_and_conservative_double_count,
        start_bucket_insn_count * (start_bucket_end_cum_cyc - bucket_start_cyc) / cast(start_bucket_cyc_count as REAL) as linear_interp_start_insns,
        end_bucket_insn_count * (bucket_end_cyc - end_bucket_start_cum_cyc) / cast(end_bucket_cyc_count as REAL) as linear_interp_end_insns
    from joined_buckets_to_ranges
), buckets_insns as (
    select
    -- calculate instruction count for the bucket for the 3 strategies.
        *,
        linear_interp_start_insns + interior_insns + linear_interp_end_insns - linear_and_conservative_double_count as linear_interp_insns,
        interior_insns as conservative_insns,
        start_bucket_insn_count + interior_insns + end_bucket_insn_count - linear_and_conservative_double_count as optimistic_insns
    from buckets_ranges_insns
), buckets_ipc as (
    select
    -- calculate ipc for the 3 strategies
        *,
        cast(linear_interp_insns as real) / bucket_cycles as linear_interp_ipc,
        cast(conservative_insns as real) / bucket_cycles as conservative_ipc,
        cast(optimistic_insns as real) / bucket_cycles as optimistic_ipc
    from buckets_insns
), buckets_ipc_time as (
    select
        start_bucket_start_time + cast(
            (
                (start_bucket_end_time - start_bucket_start_time) * -- duration of start range
                ((bucket_start_cyc - cast(start_bucket_start_cum_cyc as real)) /
                    start_bucket_cyc_count) -- percentage through start range
            )
            as biginteger
        ) as bucket_start_time,
        end_bucket_start_time + cast(
            (
                (end_bucket_end_time - end_bucket_start_time) * -- duration of end range
                ((bucket_end_cyc - cast(end_bucket_start_cum_cyc as real)) /
                    end_bucket_cyc_count) -- percentage through start range
            )
            as biginteger
        ) as bucket_end_time,
        *
    from buckets_ipc
), buckets_plot as (
    select
        bucket_start_time,
        bucket_end_time,
        bucket_start_cyc,
        bucket_end_cyc,
        bucket_spans_ranges,
        linear_interp_ipc,
        conservative_ipc,
        optimistic_ipc
    from buckets_ipc_time
)
--select * from (values (1)) where column1 = 2
select * from buckets_plot order by bucket_start_cyc
;
        