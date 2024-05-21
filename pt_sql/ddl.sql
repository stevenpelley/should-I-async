
DROP INDEX IF EXISTS samples_time;
CREATE INDEX samples_time ON samples (time);

DROP INDEX IF EXISTS pcpid_idx;
CREATE INDEX pcpid_idx ON calls (parent_call_path_id);
DROP INDEX IF EXISTS pid_idx;
CREATE INDEX pid_idx ON calls (parent_id);

DROP TABLE IF EXISTS timestamped_ranges;
CREATE TABLE timestamped_ranges AS
with t1 as (
    select
    -- only care about the samples where we have a cycle or instruction count,
    -- which will also provide new timestamps
        id,
        time,
        insn_count,
        cyc_count,
        event,
        symbol,
        to_symbol,
        branch_type_name
    from samples_view
    where insn_count > 0 or cyc_count > 0
), t2 as (
    select
    -- calculate cumulative instructions and cycles.  Cumulative cycles acts as
    -- a more precise timestamp.  Cumulative instrucitons are needed to
    -- calculate sum of instructions across many samples.
        *,
        time - (select min(time) from t1) as offset_time,
        sum(insn_count) over win as cumulative_insn_count,
        sum(cyc_count) over win as cumulative_cyc_count,
        row_number() over win as sample_row_number
    from t1
    WINDOW win as (order by id range unbounded preceding)
), t3 as (
    select
    -- turn adjacent samples into time ranges.
    -- end time/cycles of a time_range belongs to the range
    -- start time/cycles of a time_range belongs to the previous range
        lag(id) over (order by id) as start_sample_id,
        lag(time) over (order by id) as start_time,
        lag(offset_time) over (order by id) as start_offset_time,
        lag(cumulative_insn_count) over (order by id) as start_cum_insns,
        lag(cumulative_cyc_count) over (order by id) as start_cum_cyc,
        id as end_sample_id,
        time as end_time,
        offset_time as end_offset_time,
        cumulative_insn_count as end_cum_insns,
        cumulative_cyc_count as end_cum_cyc,
        sample_row_number as end_sample_row_number,
        insn_count,
        cyc_count,
        event,
        symbol,
        to_symbol,
        branch_type_name
    from t2
)
select * from t3;

CREATE UNIQUE INDEX timestamped_ranges_idx_start_cum_cyc on timestamped_ranges (start_cum_cyc);
CREATE UNIQUE INDEX timestamped_ranges_idx_end_cum_cyc on timestamped_ranges (end_cum_cyc);