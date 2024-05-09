-- define time buckets
-- assign instructions to buckets using interpolation, conservative, and
-- optimistic strategies; see comments below in query
-- calculate IPC for each bucket and each strategy
with timestamped_samples as (
    select
    -- only care about the samples where we have a cycle or instruction count,
    -- which will also provide new timestamps
        id,
        time,
        insn_count,
        cyc_count
    from samples_view
    where insn_count > 0 or cyc_count > 0
), timestamped2 as (
    select
    -- calculate cumulative instructions and cycles.  Cumulative cycles acts as
    -- a more precise timestamp.  Cumulative instrucitons are needed to
    -- calculate sum of instructions across many samples.
        *,
        time - (select min(time) from timestamped_samples) as offset_time,
        sum(insn_count) over (order by id range unbounded preceding) as cumulative_insn_count,
        sum(cyc_count) over (order by id range unbounded preceding) as cumulative_cyc_count
    from timestamped_samples
), time_ranges as (
    select
    -- turn adjacent samples into time ranges.
    -- end time/cycles of a time_range belongs to the range
    -- start time/cycles of a time_range belongs to the previous range
        lag(id) over (order by id) as start_sample_id,
        lag(offset_time) over (order by id) as start_time,
        lag(cumulative_insn_count) over (order by id) as start_cum_insns,
        lag(cumulative_cyc_count) over (order by id) as start_cum_cyc,
        id as end_sample_id,
        offset_time as end_time,
        cumulative_insn_count as end_cum_insns,
        cumulative_cyc_count as end_cum_cyc,
        insn_count,
        cyc_count
    from timestamped2
), buckets as (
    select
    -- define the buckets for our plot.  We'll use the time ranges to assign
    -- instructions and cycles to buckets.
    --
    -- note that this can be turned into more of a rolling window by using
    -- bucket_cycles that is larger than the "step".  Buckets will then overlap
        gs.value as bucket_start_cyc, 
        gs.value + 250 as bucket_end_cyc,
        250 as bucket_cycles
    from generate_series(0, 2000, 250) as gs
), joined_buckets_to_ranges as (
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
        t1.end_sample_id = t2.end_sample_id as is_start_end_same
    from
        buckets as b left outer join time_ranges as t1
        ON b.bucket_start_cyc between (t1.start_cum_cyc + 1) and t1.end_cum_cyc
        left outer join time_ranges as t2
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
)
--select * from buckets_ipc order by bucket_start_cyc
select 1
;


-- examine errors by calculating gaps between errors and joining to the
-- previous, matching, and next samples by time.
--
-- analysis: for my trace there is always a matching 'begin trace' sample.
-- there might be multiple joined samples if multiple packets were sent with the same timestamp.
with error_and_next as (
    select
        (select min(s.time) from samples_view as s where s.time > a.time) as next_sample_time,
        (select s.time from samples_view as s where s.time = a.time) as matching_sample_time,
        (select max(s.time) from samples_view as s where s.time < a.time) as prev_sample_time,
        *
    from auxtrace_errors_view as a
), with_gaps as (
    select
        next_sample_time - error_and_next.time as next_gap_duration,
        error_and_next.time - prev_sample_time as prev_gap_duration,
        *
    from error_and_next
), timestamped_samples as (
    select *
    from samples_view
    where insn_count > 0 or cyc_count > 0
), error_samples as (
    select
        s1.id as next_sample_id,
        s1.branch_type_name as next_branch_type,
        s2.id as prev_sample_id,
        s2.branch_type_name as prev_branch_type,
        s3.id as matching_sample_id,
        s3.branch_type_name as matching_branch_type,
        t.*
    from
        with_gaps as t
        left outer join timestamped_samples as s1 on next_sample_time = s1.time
        left outer join timestamped_samples as s2 on prev_sample_time = s2.time
        left outer join timestamped_samples as s3 on matching_sample_time = s3.time
)
--select * from error_samples order by id
select 1
;

-- make sure that errors and 'trace begin' are 1:1
-- it is 1:1 if this returns 0 rows
-- full outer join not supported wtf.  Emulate with union all'ed left outer
-- joins
with my_samples as (
    select * from samples_view where branch_type_name='trace begin'
), e_left as (
    select
        e.time as err_time,
        s.time as sample_time,
        e.id as err_id,
        s.id as sample_id
    FROM
        auxtrace_errors_view as e left outer join my_samples as s ON e.time = s.time
), s_left as (
    select
        e.time as err_time,
        s.time as sample_time,
        e.id as err_id,
        s.id as sample_id
    FROM
        my_samples as s left outer join auxtrace_errors_view as e ON e.time = s.time
), full_outer as (
    select * from e_left
    UNION ALL
    select * from s_left
)
select *
FROM full_outer
WHERE err_time is null OR sample_time is null
order by err_time, sample_time
;

-- calculate durations from (start OR trace begin) to (last sample before next trace begin OR end)
-- the above query proved that errors are 1:1 with 'trace begin' samples so we will use only 'trace begin'
-- note that we don't bother calculating the last end of the last range since the trial would have been winding
-- down and it won't be representative anyways.
--
-- analysis: this lets us get a range without errors that is long and isn't too
-- close to the beginning or end.
-- note that selecting a long range without errors might already bias the
-- sample: if there's no overflow packet this might imply that instruction
-- execution was slow and so tracing was able to keep up.
--
-- Eventually I'll need to find a way to select ranges at random and compare them to see if this is true.
with range_starts as (
    select id, time from samples_view where branch_type_name='trace begin'
    UNION ALL
    select id, time
    from samples_view
    WHERE
        time = (select min(time) from samples_view where insn_count > 0 or cyc_count > 0) 
        AND (insn_count > 0 or cyc_count > 0)
)
, range_starts_with_next as (
    select
        id,
        time,
        lead(id) over (order by id) as next_id,
        lead(time) over (order by id) as next_time
    from range_starts
)
, timestamped_samples as (
    select
        id,
        time
    from samples_view
    where insn_count > 0 or cyc_count > 0
)
, ranges as (
    select
    -- contains all possible timestamped samples in the range.  We'll take the max time next
        r.time as start_time,
        (select max(s.time) from timestamped_samples as s where s.time < r.next_time) as end_time
    FROM
        range_starts_with_next as r
), ranges_with_duration as (
    select
        end_time - start_time as duration,
        start_time,
        end_time,
        (select min(s.id) from timestamped_samples as s where s.time = start_time) as start_id,
        (select max(s.id) from timestamped_samples as s where s.time = end_time) as end_id,
        start_time - (select min(s.time) from samples as s where s.time > 0) as time_after_start,
        (select max(s.time) from samples as s) - end_time as time_to_end
    from ranges
)
select * from ranges_with_duration order by duration desc limit 10
;
        
        
