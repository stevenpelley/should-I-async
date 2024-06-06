# %%
# imports

import numpy
from matplotlib import pyplot as plt
import matplotlib
import iptdataframes.util as iptutil
import duckdb
import collections
conn = None

# %%
# definitions

# set which database file to use
# db_file = "databases/async.duckdb"
db_file = "databases/async_retcomp.duckdb"
# db_file = "databases/sync.duckdb"
# db_file = "databases/sync_retcomp.duckdb"
# db_file = "databases/sync_retcomp_kernelonly_noswitchevents.duckdb"

if 'conn' in locals() and conn is not None:
    conn.close()
conn = iptutil.SqlUtil(duckdb.connect(db_file))

# set up the moving average
window_size = 2000

long_sample_limit = 500

# throw out longest samples totalling 1% of time for cdf
cumulative_longest_time_to_discard = 0.01

sample_count = conn.execute('''--sql
select count(*) from samples
;''')
sample_count

# %%
# setup

# load the time bounds and figure out where our plots should focus.
with open("sql/timestamped_ranges.sql") as script:
    conn.execute_script(script.read())
    sql_script = script.read()

# set the time bounds
# calculate ipc over buckets
# start_ns, end_ns = 4472714308357, 4472714316324

first_time = conn.execute('''--sql
SELECT min(end_time)
FROM timestamped_ranges
WHERE end_time > 0
;''').iloc[0, 0]

last_time = conn.execute('''--sql
SELECT max(end_time)
FROM timestamped_ranges
;''').iloc[0, 0]

start_ns = int(first_time + ((last_time - first_time) / 2))
duration_ns = 15_000
end_ns = start_ns + duration_ns

conn.execute('''--sql
CREATE OR REPLACE TABLE time_bounds AS
SELECT
    col0 AS ns,
    (SELECT MIN(end_cum_cyc)
        FROM timestamped_ranges
        WHERE end_time > col0
        ) AS cyc
FROM (VALUES ({}), ({}))
;'''.format(start_ns, end_ns))

range_data = conn.execute('''--sql
SELECT cyc
FROM time_bounds
ORDER BY cyc
;''')
start_cycles, end_cycles = map(int, range_data.iloc[:, 0])

# %%
# timestamped ranges

# perform data preparation.  This makes faster to work on plots so that each
# attempt doesn't have to wait for queries

# locate syscalls
with open("sql/callstacks.sql") as script:
    text = script.read()
    sql_script = text.format(**{
        "start_ns": start_ns,
        "end_ns": end_ns,
    })
    conn.execute_script(sql_script)

conn.execute('''--sql
CREATE OR REPLACE VIEW samples_for_cdf AS
WITH t1 as (
    SELECT
        insn_count,
        cyc_count
    FROM timestamped_ranges
    WHERE branch_type_name <> 'trace begin'
), t2 as (
    SELECT
        insn_count,
        cyc_count,
        sum(cyc_count) OVER (ORDER BY cyc_count DESC) as cum_cycles,
        cum_cycles / cast( (select sum(cyc_count) from t1) AS REAL) as cum_cycles_perc
    FROM t1
)
SELECT
    insn_count,
    cyc_count,
    cum_cycles,
    cum_cycles_perc
FROM t2
WHERE cum_cycles_perc > {cumulative_longest_time_to_discard}
;'''.format(cumulative_longest_time_to_discard=cumulative_longest_time_to_discard))

conn.execute('''--sql
CREATE OR REPLACE TABLE range_cdf as
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
;''')

conn.execute('''--sql
-- first treat this point as the leading edge of the sliding window
-- duckdb is incorrectly pushing filters into the ASOF join and then giving an error.
-- materialize this in a temp table and then filter that.
CREATE OR REPLACE TABLE sliding_window AS
with t1 as (
    SELECT
        leading_ranges.end_cum_cyc = trailing_ranges.end_cum_cyc AS is_single_range,

        leading_ranges.end_cum_cyc   AS window_end_cum_cyc,
        window_end_cum_cyc - ({window_size} - 1) AS window_start_cum_cyc,
        leading_ranges.end_time AS end_time,

        leading_ranges.end_cum_cyc   AS leading_end_cum_cyc,
        leading_ranges.cyc_count     AS leading_cyc_count,
        leading_ranges.end_cum_insns AS leading_end_cum_insns,
        leading_ranges.insn_count    AS leading_insn_count,

        trailing_ranges.end_cum_cyc   AS trailing_end_cum_cyc,
        trailing_ranges.cyc_count     AS trailing_cyc_count,
        trailing_ranges.end_cum_insns AS trailing_end_cum_insns,
        trailing_ranges.insn_count    AS trailing_insn_count,

        'leading' AS side,
        -- aliased implies that constructing spans from the other endpoint (the
        -- other side of the UNION ALL) will create the same span and so we must
        -- account for duplicates.
        window_start_cum_cyc = trailing_end_cum_cyc AS is_span_aliased,
    FROM 
        timestamped_ranges AS leading_ranges
        ASOF LEFT JOIN timestamped_ranges AS trailing_ranges
            -- looking for the range overlapping the trailing window
            -- if trailing.end_cum_cyc=leading_ranges.end_cum_cyc we have perfect alignment,
            -- otherwise there is some overlap
            ON trailing_ranges.end_cum_cyc > (leading_ranges.end_cum_cyc - {window_size})

    UNION ALL

    -- now treat this point as the trailing edge of the window
    -- we synthesize a transition point
    SELECT
        -- cannot happen as the next cycle is introducing a new range and span,
        -- by definition
        FALSE AS is_single_range,

        trailing_ranges.end_cum_cyc + {window_size} - 1 AS window_end_cum_cyc,
        trailing_ranges.end_cum_cyc AS window_start_cum_cyc,
        leading_ranges.end_time AS end_time,

        leading_ranges.end_cum_cyc   AS leading_end_cum_cyc,
        leading_ranges.cyc_count     AS leading_cyc_count,
        leading_ranges.end_cum_insns AS leading_end_cum_insns,
        leading_ranges.insn_count    AS leading_insn_count,

        trailing_ranges.end_cum_cyc   AS trailing_end_cum_cyc,
        trailing_ranges.cyc_count     AS trailing_cyc_count,
        trailing_ranges.end_cum_insns AS trailing_end_cum_insns,
        trailing_ranges.insn_count    AS trailing_insn_count,

        'trailing' AS side,
        -- aliased implies that constructing spans from the other endpoint (the
        -- other side of the UNION ALL) will create the same span and so we must
        -- account for duplicates.
        window_end_cum_cyc = leading_end_cum_cyc AS is_span_aliased,
    FROM 
        timestamped_ranges AS trailing_ranges
        ASOF JOIN timestamped_ranges AS leading_ranges
            -- now treat the trailing_ranges as the trailing edge of the window
            ON leading_ranges.end_cum_cyc >= (trailing_ranges.end_cum_cyc + {window_size} - 1)
)
SELECT
    *
FROM t1
-- BUG: it tries to push this into the ASOF JOIN and then concludes that it is
-- an invalid ASOF JOIN condition.
--
-- 1. semantically, WHERE should apply after FROM/JOIN, so it is not a condition on the join
-- 2. if applied in an outer query block it clearly has no relation to the join.
-- so I believe the filter is getting incorrectly pushed into the join.
--WHERE
--    side='leading' OR
--    -- the first SELECT of the UNION ALL already produced this point
--    trailing_end_cum_cyc <> leading_end_cum_cyc - {window_size}
;'''.format(
    window_size=window_size
))

conn.execute('''--sql
CREATE OR REPLACE TABLE ipc_spans AS
with t1 as (
    SELECT
        *
    FROM sliding_window
WHERE NOT (side = 'trailing' AND is_span_aliased)
), t2 as (
    -- calculate instructions and cycles as of the last (latest/leading edge)
    -- window in this span
    --
    -- calculate for the leading, trailing, and interior ranges separately
    -- this gives us a slope of the linear interp IPC for the period ending at
    -- leading_end_cum_cyc
    -- it also gives us the optimistic and conservative IPC over that same span
    SELECT
        *,

        trailing_insn_count AS trailing_insns,
        -- number of cycles from the trailing sample that fall within the window
        trailing_end_cum_cyc - window_start_cum_cyc + 1 AS trailing_cyc_in_window,

        -- if the entire window is contained within a single sample we will call
        -- this the trailing edge and leave the leading edge 0 to make sure we
        -- don't double-count
        IF(is_single_range,
            0,
            leading_insn_count
            ) AS leading_insns,
        -- number of cycles from the leading sample that fall within the window
        IF(is_single_range,
            0,
            leading_cyc_count - (leading_end_cum_cyc - window_end_cum_cyc)
            ) AS leading_cyc_in_window,

        IF(is_single_range,
            0,
            (leading_end_cum_insns - leading_insn_count) - trailing_end_cum_insns
            ) AS interior_insns,
        IF(leading_end_cum_cyc = trailing_end_cum_cyc,
            0,
            (leading_end_cum_cyc - leading_cyc_count) - trailing_end_cum_cyc
            ) AS interior_cyc,

        CAST(trailing_insns + leading_insns + interior_insns as real) / ({window_size}) AS optimistic_ipc,
        CAST(interior_insns as real) / ({window_size}) AS conservative_ipc,
        (
            (trailing_insns * (CAST(trailing_cyc_in_window as real) / trailing_cyc_count)) +
            (leading_insns * (CAST(leading_cyc_in_window as real) / leading_cyc_count)) +
            interior_insns
        ) / ({window_size}) AS end_linear_interp_ipc,
    FROM t1
), t3 as (
    SELECT
        *,
        -- pandas casts any column containing null to float because it treats it as NaN.
        -- wtf who uses this shit.  A missing integer is perfectly reasonable
        COALESCE(
            window_end_cum_cyc - LAG(window_end_cum_cyc, 1) OVER (
                ORDER BY window_end_cum_cyc),
            0
            ) AS span_width_cyc,
    FROM t2
)
SELECT
    -- things needed for plotting
    -- casting to bigint as duckdb infers huge ints (int128) which pandas does
    -- not support and so casts to floats
    CAST(window_end_cum_cyc AS bigint) AS window_end_cum_cyc,
    CAST(window_start_cum_cyc AS bigint) AS window_start_cum_cyc,
    CAST(end_time AS ubigint) AS end_time,
    conservative_ipc,
    optimistic_ipc,
    -- make it stand out if this is missing due to division by 0
    coalesce(end_linear_interp_ipc, 10000.0) AS end_linear_interp_ipc,

    CAST(span_width_cyc AS bigint) AS span_width_cyc,
    CAST(window_end_cum_cyc - span_width_cyc AS bigint) AS span_start_cyc,

    -- helpful for debugging
    leading_cyc_in_window,
    leading_cyc_count,
    leading_insns,
    trailing_cyc_in_window,
    trailing_cyc_count,
    trailing_insns,
    interior_cyc,
    interior_insns,

    leading_end_cum_cyc,
    leading_end_cum_insns,
    leading_cyc_count,
    leading_insn_count,

    trailing_end_cum_cyc,
    trailing_end_cum_insns,
    trailing_cyc_count,
    trailing_insn_count,
FROM t3
ORDER BY window_end_cum_cyc
;'''.format(window_size=window_size))

# suppress printing
pass

# %%
# time series prep

# convert cyc to fractional ns and get the cyc time bounds
time_cyc_data = conn.execute('''--sql
SELECT ns::ubigint, cyc::ubigint
FROM time_bounds
ORDER BY cyc
;''')
((start_ns, start_cyc,), (end_ns, end_cyc,)) = time_cyc_data.iloc
ns_rate = float(end_ns - start_ns) / (end_cyc - start_cyc)


def duration_cyc_to_ns(duration_cyc):
    return duration_cyc * ns_rate


def time_cyc_to_ns(cyc):
    return start_ns + duration_cyc_to_ns(cyc - start_cyc)


# %%
# time series plots

# %matplotlib widget

# plot 1: individual samples

plt.close()
fig, (ax1, ax2, ax3) = plt.subplots(
    nrows=3,
    sharex=True,
    layout='constrained')

# plot 1: raw timestamped ranges
range_data = conn.execute('''--sql
SELECT
    start_cum_cyc,
    end_cum_cyc,
    cast(insn_count as real) / cyc_count as ipc,
    cyc_count,
FROM timestamped_ranges
WHERE end_cum_cyc BETWEEN {} AND {}
ORDER BY end_cum_cyc
;'''.format(start_cyc, end_cyc))
range_data['start_ns'] = time_cyc_to_ns(range_data['start_cum_cyc'])
range_data['end_ns'] = time_cyc_to_ns(range_data['end_cum_cyc'])
ax1.hlines(
    y='ipc',
    xmin='start_ns',
    xmax='end_ns',
    data=range_data,
)
ax1.set_title('Individual timestamped samples')
ax1.set_ylabel('IPC')

# plot 2: rolling average and error bounds

span_data = conn.execute('''--sql
SELECT *
FROM ipc_spans
WHERE window_end_cum_cyc BETWEEN {} AND {}
ORDER BY window_end_cum_cyc
;'''.format(start_cyc, end_cyc))
span_data['end_ns'] = time_cyc_to_ns(span_data['window_end_cum_cyc'])

plot_handle = ax2.plot(
    'end_ns',
    'end_linear_interp_ipc',
    data=span_data,
    label='interpolated ipc')

# plot the ipc range rectangles
span_data['span_start_ns'] = time_cyc_to_ns(span_data['span_start_cyc'])
span_data['span_width_ns'] = duration_cyc_to_ns(
    span_data['span_width_cyc'])
span_data['ipc_range'] = span_data['optimistic_ipc'] - \
    span_data['conservative_ipc']

patches = []
for index, row in span_data.iterrows():
    patches.append(matplotlib.patches.Rectangle(
        (row['span_start_ns'], row['conservative_ipc'],),
        row['span_width_ns'],
        row['ipc_range'],
    ))
patches_collection = matplotlib.collections.PatchCollection(
    patches,
    alpha=0.5,
    color='tab:cyan')
ax2.add_collection(patches_collection)
proxy_patch = matplotlib.patches.Patch(
    alpha=0.5,
    color='tab:cyan',
    label='ipc error bound')

# draw in individual ranges with durations exceeding some threshold
range_data['start_ns'] = time_cyc_to_ns(range_data['start_cum_cyc'])
range_data['end_ns'] = time_cyc_to_ns(range_data['end_cum_cyc'])

long_sample_limit
long_range_data = range_data[range_data.cyc_count > long_sample_limit]
hlines_handle = ax2.hlines(
    y='ipc',
    xmin='start_ns',
    xmax='end_ns',
    data=long_range_data,
    color='r',
    label='ranges > {}'.format(long_sample_limit),
)
ax2.set_title('Rolling average (window={} cycles)'.format(window_size))
ax2.set_ylabel('IPC')
ax2.legend(
    handles=[plot_handle[0], hlines_handle, proxy_patch],
)

# plot 3: significant events timeline

conn.execute('''--sql
CREATE OR REPLACE TABLE full_syscalls AS
WITH RECURSIVE call_hierarchy(
        group_id,
        id,
        parent_id,
        depth,
        call_path_id,
        call_time,
        return_time,
        ) as (
    -- base case
    SELECT
        id,
        id,
        parent_id,
        0,
        call_path_id,
        call_time,
        return_time,
    FROM calls

    UNION ALL

    SELECT
        children.group_id,
        parents.id,
        parents.parent_id,
        children.depth + 1,
        parents.call_path_id,
        parents.call_time,
        parents.return_time,
    FROM call_hierarchy AS children
        INNER JOIN calls AS parents
        ON children.parent_id = parents.id
    -- 0 has parent_id 0, so filter this out so that it terminates
    WHERE children.id <> 0
), call_hierarchy_with_symbols as (
    SELECT
        call_hierarchy.*,
        call_paths.symbol_id AS symbol_id,
        symbols.name         AS symbol,
        symbols.dso_id       AS dso_id,
        dsos.short_name      AS dso,
    FROM call_hierarchy
        LEFT JOIN call_paths ON call_hierarchy.call_path_id = call_paths.id
        LEFT JOIN symbols    ON call_paths.symbol_id = symbols.id
        LEFT JOIN dsos       ON symbols.dso_id = dsos.id
), filtered as (
SELECT *
FROM call_hierarchy_with_symbols
WHERE
    ((call_time BETWEEN {start_ns} AND {end_ns})
    OR (return_time BETWEEN {start_ns} AND {end_ns}))
), agged AS (
    SELECT
        group_id,
        max(depth) AS d,
        first(call_time ORDER BY depth) AS first_call_time,
        first(return_time ORDER BY depth) AS first_return_time,
        first(symbol ORDER BY depth) AS first_symbol,
        first(dso ORDER BY depth) as first_dso,
        last(call_time ORDER BY depth) AS last_call_time,
        last(return_time ORDER BY depth) AS last_return_time,
        last(symbol ORDER BY depth) AS last_symbol,
        last(dso ORDER BY depth) as last_dso,
        array_agg(symbol ORDER BY depth) as symbol_list,
        any_value(call_time) FILTER (depth = 3) as syscall_entry_call_time,
        any_value(return_time) FILTER (depth = 3) as syscall_entry_return_time,
    FROM filtered 
    GROUP BY group_id
    HAVING 1=1
        AND list_slice(symbol_list, 2, 4) = list_value('x64_sys_call', 'do_syscall_64', 'entry_SYSCALL_64')
)
SELECT
    syscall_entry_call_time AS call_time,
    syscall_entry_return_time AS return_time,
    first_symbol AS syscall_kernel_name,
    group_id AS call_id,
FROM agged
;'''.format(end_ns=end_ns, start_ns=start_ns))

# compute context switches

conn.execute('''--sql
CREATE OR REPLACE TABLE context_switches AS
WITH prepare_task_switch AS (
    SELECT
        calls.id AS pts_call_id,
        calls.call_time AS pts_call_time,
        calls.return_time AS pts_call_return_time,
    FROM
        calls
        LEFT JOIN call_paths ON calls.call_path_id = call_paths.id
        LEFT JOIN symbols ON call_paths.symbol_id = symbols.id
        LEFT JOIN dsos ON symbols.dso_id = dsos.id
    WHERE
        symbols.name = 'prepare_task_switch'
        AND dsos.short_name = '[kernel.kallsyms]'
), switch_mm_irqs_off AS (
    SELECT
        calls.id AS smio_call_id,
        calls.call_time AS smio_call_time,
        calls.return_time AS smio_call_return_time,
    FROM
        calls
        LEFT JOIN call_paths ON calls.call_path_id = call_paths.id
        LEFT JOIN symbols ON call_paths.symbol_id = symbols.id
        LEFT JOIN dsos ON symbols.dso_id = dsos.id
    WHERE
        symbols.name = 'switch_mm_irqs_off'
        AND dsos.short_name = '[kernel.kallsyms]'
)
SELECT
    *
FROM
    prepare_task_switch AS pts ASOF
    LEFT JOIN switch_mm_irqs_off AS smio
        ON pts.pts_call_time < smio.smio_call_time
;''')

context_switches = conn.execute('''--sql
SELECT
    smio_call_time AS time
FROM context_switches
WHERE time BETWEEN {start_ns} AND {end_ns}
;'''.format(end_ns=end_ns, start_ns=start_ns))

conn.execute('''--sql
CREATE OR REPLACE TABLE on_cpu_syscalls AS
WITH in_view_context_switches AS (
    SELECT
        smio_call_time AS time,
        NULL AS name,
        NULL AS call_id
    FROM context_switches
    WHERE time BETWEEN {start_ns} AND {end_ns}
), range_starts AS (
    SELECT * from in_view_context_switches
    
    UNION ALL
    
    SELECT
        call_time AS time,
        syscall_kernel_name AS name,
        call_id AS call_id,
    FROM full_syscalls
), range_ends AS (
    SELECT * from in_view_context_switches
    
    UNION ALL
    
    SELECT
        return_time AS time,
        syscall_kernel_name AS name,
        call_id AS call_id,
    FROM full_syscalls
)
SELECT
    range_starts.time AS starts_time,
    range_starts.name AS starts_name,
    range_starts.call_id AS starts_call_id,
    range_ends.time AS ends_time,
    range_ends.name AS ends_name,
    range_ends.call_id AS ends_call_id,
FROM
    range_starts
    ASOF JOIN range_ends
        ON range_ends.time > range_starts.time
;'''.format(end_ns=end_ns, start_ns=start_ns))

# assert that this looks good
assert_df = conn.execute('''--sql
SELECT *
FROM on_cpu_syscalls
WHERE
    (starts_time BETWEEN {start_ns} AND {end_ns}
        OR ends_time BETWEEN {start_ns} AND {end_ns})
    AND
    (
        (starts_call_id IS NULL AND ends_call_id IS NULL)
        OR (starts_call_id IS NOT NULL AND ends_call_id IS NOT NULL AND starts_call_id <> ends_call_id)
    )
ORDER BY starts_time
;'''.format(end_ns=end_ns, start_ns=start_ns))
assert len(assert_df) == 0, (
    "inconsistent on-cpu syscalls:\n{}".format(assert_df.to_string()))

on_cpu_syscalls = conn.execute('''--sql
SELECT
    starts_time,
    ends_time,
    coalesce(starts_name, ends_name) AS name
FROM on_cpu_syscalls
WHERE
    (starts_time BETWEEN {start_ns} AND {end_ns}
        OR ends_time BETWEEN {start_ns} AND {end_ns})
-- order by name to make legend markers more deterministic
ORDER BY name
;'''.format(end_ns=end_ns, start_ns=start_ns))


d = collections.defaultdict(list)
for t in on_cpu_syscalls.itertuples(index=False):
    label = t[2].lstrip('_')
    d[label].append(t[0:2])
labels = []
handles = []
hatches = ['/', '\\', 'x']
colors = ['orange', 'green', 'blue', 'red']
span_type_count = 0
for label, spans in d.items():
    labels.append(label)
    for i, span in enumerate(spans):
        handle = ax3.axvspan(
            xmin=span[0],
            xmax=span[1],
            facecolor=colors[span_type_count % len(colors)],
            hatch=hatches[span_type_count % len(hatches)]
        )
        if i == 0:
            handles.append(handle)
    span_type_count += 1

# plot context switches
labels.append('context switches')
for i, *switch in context_switches.itertuples(index=True):
    handle = ax3.axvline(
        x=switch[0],
        color='r'
    )
    if i == 0:
        handles.append(handle)

# find and plot overflow errors
have_errors_table = conn.execute('''--sql
select count(*)
from information_schema.tables
where table_name = 'auxtrace_errors'
;''').iloc[0, 0] > 0

if have_errors_table:
    conn.execute('''--sql
-- duckdb incorrectly pushes predicates into the ASOF join, where they join the
-- left table to the incorrect rows of the right table instead of filtering
-- those rows.  Create a temp table and then filter later instead
CREATE TEMP TABLE errors_and_ranges AS
SELECT
    ae.time,
    tr.start_time,
    tr.start_cum_cyc,
    tr.end_time,
    tr.end_cum_cyc,
    tr.cyc_count,
    tr.end_time - tr.start_time AS duration_ns,
FROM auxtrace_errors AS ae asof LEFT JOIN
    timestamped_ranges AS tr
    ON ae.time < tr.end_time
WHERE ae.msg='Overflow packet'
ORDER BY tr.end_cum_cyc
;''')

    errors = conn.execute(
        '''--sql
SELECT
    start_time,
    end_time,
FROM errors_and_ranges
WHERE
    (start_time <= {start_ns} AND end_time >= {end_ns})
    OR start_time BETWEEN {start_ns} AND {end_ns}
    OR end_time BETWEEN {start_ns} AND {end_ns}
ORDER BY end_cum_cyc
;'''.format(start_ns=start_ns, end_ns=end_ns))

    if len(errors) > 0:
        labels.append('trace overflow')
        for i, *err in errors.itertuples(index=True):
            handle = ax3.axvspan(
                xmin=err[0],
                xmax=err[1],
                facecolor=colors[span_type_count % len(colors)],
                hatch=hatches[span_type_count % len(hatches)]
            )
            if i == 0:
                handles.append(handle)
# always increment to make legend more deterministic
span_type_count += 1

ax3.legend(
    handles,
    labels,
)
# force the markers to appear at the top
ax3.set_yticks([])
ax3.set_title('syscall events')
ax3.set_xlabel("ns since host boot")

# %%
# cdfs

# 1. calculate the cdf of timestamped range durations in cycles.
# 2. plot the distributions as contour lines of instructions or IPC
# 3. plot the cdf weighted by duration.  This displays cumulative time spent in
# ranges of each duration, not simply frequency.

range_data = conn.execute('select * from range_cdf')
cyc_count, cdf, weighted_cdf, *_ = range_data[range_data['cdf'] > 0.95].T.iloc

plt.close()
fig, (ax1, ax2, ax3) = plt.subplots(
    nrows=3, sharex=True, layout='constrained')
ax1.semilogx(cyc_count, cdf)
ax1.set_title('CDF of sample range durations (cycles)')
ax2.semilogx(cyc_count, weighted_cdf)
ax2.set_title('CDF, weighted by duration')

min_cyc_count = cyc_count.min()

range_data = conn.execute("""--sql
with t1 as (
SELECT
    cyc_count,
    insn_count,
    cast(insn_count as real) / cyc_count as ipc,
    CASE
        WHEN insn_count <= 35 THEN 0
        WHEN insn_count <= 100 THEN 1
        ELSE 2
    END AS insn_group
from samples_for_cdf
where cyc_count >= {min_cyc_count}
)
SELECT
cyc_count,
cast(count(*) FILTER (insn_group = 0) * cyc_count as real) /
    (select sum(cyc_count) from samples_for_cdf) as weighted_insn_count_0,
cast(count(*) FILTER (insn_group = 1) * cyc_count as real) /
    (select sum(cyc_count) from samples_for_cdf) as weighted_insn_count_1,
cast(count(*) FILTER (insn_group = 2) * cyc_count as real) /
    (select sum(cyc_count) from samples_for_cdf) as weighted_insn_count_2,
from t1
group by cyc_count
order by cyc_count
;""".format(min_cyc_count=min_cyc_count))

bins = numpy.logspace(
    numpy.log10(numpy.min(cyc_count)),
    numpy.log10(numpy.max(cyc_count)),
    30)
kwargs = {
    'data': range_data,
    'x': 'cyc_count',
    'bins': bins,
    # 'density': True,
}
ax3.hist(weights='weighted_insn_count_0', label='35', **kwargs)
ax3.hist(weights='weighted_insn_count_1', label='100', **kwargs)
ax3.hist(weights='weighted_insn_count_2', label='>100', **kwargs)
ax3.legend()
ax3.set_title(
    'Perc. time in samples by duration (cycles) and # instructions')
ax3.set_ylabel("% time")
ax3.set_xlabel("cycles")


# %%
# teardown
conn.close()
