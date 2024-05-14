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
select * from (values (1)) where column1 = 2
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
--select * from ranges_with_duration order by duration desc limit 10
select * from (values (1)) where column1 = 2
;