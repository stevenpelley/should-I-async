-- SQLite
with timestamped_samples as (
    select
        id,
        time,
        insn_count,
        cyc_count
    from samples_view
    where insn_count > 0 or cyc_count > 0
), timestamped2 as (
    select
        *,
        time - (select min(time) from timestamped_samples) as offset_time,
        sum(insn_count) over (order by id range unbounded preceding) as cumulative_insn_count,
        sum(cyc_count) over (order by id range unbounded preceding) as cumulative_cyc_count
    from timestamped_samples
), time_ranges as (
    -- end time/cycles of a time_range belongs to the range
    -- start time/cycles of a time_range belongs to the previous range
    select
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
        gs.value as bucket_start_cyc, 
        gs.value + 250 as bucket_end_cyc
    from generate_series(0, 2000, 250) as gs
), joined_buckets_to_ranges as (
    select
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
        t2.cyc_count       as end_bucket_cyc_count
    from buckets as b left outer join time_ranges as t1 left outer join time_ranges as t2
    where
        b.bucket_start_cyc between (t1.start_cum_cyc + 1) and t1.end_cum_cyc
        and
        b.bucket_end_cyc between (t2.start_cum_cyc + 1) and t2.end_cum_cyc
)
select * from joined_buckets_to_ranges order by bucket_start_cyc
--select 1
--select min(time) from samples_view where command='csgb'
--select * from timestamped2 where time > 259034356306612 order by id limit 1
;

--select * from samples_view where id in (95, 98);
--select * from samples_view order by cyc_count desc limit 100;

--select * from comm_threads_view where pid = 194955 order by command, pid, thread_id;
--select * from context_switches_view where cpu=0 order by time;

-- pid 194955 swaps out as csgb at 259033364173326 whereas prior to that it swaps as taskset
-- clearly it execs, the samples table/view may only join it as the last command name
-- confirmed: timeout, taskset, and csgb are all pid 194955
--
-- conclusion: don't trust comm in samples/samples_view.



-- task:
-- collect all samples belonging to a timestamp together so that we can order by the longest blocks and look for common branches (e.g., syscall return)
--
-- the longest cyc blocks contain a cbr.  Possible that they contain cbr because
-- they are long, but also that they are long because of the cbr.  It's
-- reasonable to assume that a power event changing the core's frequency would
-- have a delay
--
-- many long blocks are associated with rare events -- file system, process
-- startup and exit, page faults, malloc syscalls, perf process, irq kernel functions
--
-- I think we'll have to add some more filters to examine because there's a lot
-- of "rare noise" that turns out isn't so rare
--
-- some syscall returns contain psb, which from man page is known to cause a timing bubble.
-- I also see syscall entry with high cyc count, but many have kmalloc or other (I assume) rare events and IPC ~0.15 which isn't a huuuuge stall
--
-- there are some curious long, slow blocks in tokio calls.
--
-- near the 1000 cyc mark I see a lot of syscall returns and they all contain a psb
-- do syscall returns categorically contain a psb?
-- how do I filter for groups that have a symbol 'syscall_return_via_sysret' or branch type 'return from system call'
-- with a self join on group_sample_id looking for that branch_type_name
--
-- answer: the most expensive syscall returns have a psb or cbr, but the vast
-- majority of syscall returns have neither and are still long.
--
-- let's get an average group cyc count for these groups.
-- average is 681 cycles.  Syscall returns, at least for socket read and write
-- and recorded here, are expensive.
----with t1 as (
----    select
----        *,
----        sum(cyc_count) over (order by id range unbounded preceding) as cum_cyc_count
----    from samples_view
----), t2 as (
----    select 
----    *,
----    iif(cyc_count > 0, id, min(id) over (order by cum_cyc_count groups between current row and 1 following exclude group)) as group_sample_id,
----    iif(cyc_count > 0, cyc_count, max(cyc_count) over (order by cum_cyc_count groups between current row and 1 following exclude group)) as group_cyc_count,
----    iif(cyc_count > 0, insn_count, max(insn_count) over (order by cum_cyc_count groups between current row and 1 following exclude group)) as group_insn_count,
----    iif(cyc_count > 0, IPC, max(IPC) over (order by cum_cyc_count groups between current row and 1 following exclude group)) as group_ipc
----    from t1
----), has_syscall_return as (
----    select
----        group_cyc_count,
----        group_sample_id,
----        group_insn_count,
----        id,
----        group_ipc,
----        command,
----        pid,
----        event,
----        branch_type_name,
----        symbol,
----        dso_short_name,
----        to_symbol,
----        to_dso_short_name
----    from t2
----    where exists (select 1 from t2 as t22 where t2.group_sample_id = t22.group_sample_id and t22.branch_type_name = 'return from system call')
----)
----select
----    avg(group_cyc_count)
----from has_syscall_return
----where group_sample_id = id
----;