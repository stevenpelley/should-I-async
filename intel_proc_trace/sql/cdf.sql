DROP VIEW IF EXISTS samples_for_cdf;
CREATE VIEW samples_for_cdf AS
select
    insn_count,
    cyc_count
    --1.0/(insn_count*cyc_count) as precision
from timestamped_ranges
where 1=1
    and branch_type_name <> 'trace begin'
    ---- remove the last 1ms of the trace
    --and end_offset_time < ((select max(end_offset_time) from timestamped_ranges) - 1000000)
-- discard the greatest 10 ranges as outliers
ORDER BY cyc_count desc
OFFSET 10
;

-- calculate cdfs
DROP TABLE IF EXISTS range_cdf;
create TABLE range_cdf as
with t1 as (
    select
        cyc_count,
        count(*) c,
        count(*) * cyc_count as weighted_c,
        count(*) FILTER (WHERE insn_count <= 1) as c_insns_5,
        count(*) FILTER (WHERE insn_count <= 20) as c_insns_25,
        count(*) FILTER (WHERE insn_count <= 100) as c_insns_125
    from samples_for_cdf
    group by cyc_count
), t2 as (
    select
        *,
        sum(c) OVER win as nonnormalized_cdf,
        sum(weighted_c) OVER win as nonnormalized_weighted_cdf,
        cast(c_insns_5 as real) / c as perc_insns_5,
        cast(c_insns_25 as real) / c as perc_insns_25,
        cast(c_insns_125 as real) / c as perc_insns_125
    from t1
    WINDOW win as (order by cyc_count range unbounded preceding)
), t3 as (
    select
        cyc_count,
        cast(nonnormalized_cdf as real) / (select max(nonnormalized_cdf) from t2) as cdf,
        cast(nonnormalized_weighted_cdf as real) / (select max(nonnormalized_weighted_cdf) from t2) as weighted_cdf,
        perc_insns_5 as perc_insns_5_stacked,
        perc_insns_25 - perc_insns_5 as perc_insns_25_stacked,
        perc_insns_125 - perc_insns_25 as perc_insns_125_stacked,
        1.0 - perc_insns_125 as perc_insns_125_plus_stacked
    from t2
)
select * from t3
;