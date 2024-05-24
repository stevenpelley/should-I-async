-- notes from investigation:
-- syscalls are represented by calls to entry_SYSCALL_64.  This then calls the
-- kernel code for a syscall by calling functions do_syscall_64 and then
-- x64_sys_call.  This last function will call exactly one function of the form
-- __x64_sys_XXX where XXX is the name of the syscall.
-- The call_id of a syscall (entry_SYSCALL_64) is the sample that calls the
-- syscall.  The return_id is the sample for returning from the syscall.


-- walk the calls paths to expand into the entire stack.
-- GROUP BY group_id ORDER BY depth on this view.
DROP VIEW IF EXISTS call_path_hierarchy;
CREATE VIEW call_path_hierarchy AS
WITH RECURSIVE call_hierarchy(group_id, id, parent_id, depth, symbol_id) as (
select id,
    id,
    parent_id,
    0,
    symbol_id
from call_paths
UNION ALL
select children.group_id,
    parents.id,
    parents.parent_id,
    children.depth + 1,
    parents.symbol_id
from call_hierarchy as children
    inner join call_paths as parents on children.parent_id = parents.id
where children.id <> 0
)
select *
from call_hierarchy;

-- add dso and symbols to the call path hierarchy/stack
DROP VIEW IF EXISTS call_hierarchy_with_symbols;
CREATE VIEW call_hierarchy_with_symbols AS
select cph.*,
    d.short_name as dso_name,
    d.id as dso_id,
    s.name as symbol_name
from call_path_hierarchy as cph
    left outer join symbols as s on cph.symbol_id = s.id
    left outer join dsos as d on s.dso_id = d.id;

-- locate the entry_SYSCALL_64 call paths alongside the kernel syscall function
-- symbol for the call
DROP VIEW IF EXISTS syscall_call_path_to_syscall_kernel_name;
CREATE VIEW syscall_call_path_to_syscall_kernel_name AS
with t as (
select
    group_id,
    depth,
    symbol_name,
    id,
    lag(symbol_name, 1) OVER win as symbol_lag_1,
    lag(id, 1) OVER win as call_path_id_lag_1,
    lag(symbol_name, 2) OVER win as symbol_lag_2,
    lag(id, 2) OVER win as call_path_id_lag_2,
    lag(symbol_name, 3) OVER win as symbol_lag_3,
    lag(id, 3) OVER win as call_path_id_lag_3
FROM call_hierarchy_with_symbols
WINDOW win as (PARTITION BY group_id ORDER BY depth)
)
select
    id as call_path_id,
    symbol_lag_3 as syscall_kernel_name
from t
WHERE
    symbol_name = 'entry_SYSCALL_64'
    and depth = 3
    and symbol_lag_1 = 'do_syscall_64'
    and symbol_lag_2 = 'x64_sys_call'
;

DROP VIEW IF EXISTS syscall_calls;
CREATE VIEW syscall_calls AS
select
    *
from calls_view INNER JOIN syscall_call_path_to_syscall_kernel_name as syscalls USING (call_path_id)
order by id
;

DROP TABLE IF EXISTS syscall_call_ret;
CREATE TABLE syscall_call_ret AS
select
    'CALL' as event,
    call_time as time,
    syscall_kernel_name
from syscall_calls
where call_time between {start_ns} AND {end_ns}
UNION ALL
select
    'RET' as event,
    return_time as time,
    syscall_kernel_name
from syscall_calls
where return_time between {start_ns} AND {end_ns}
;


-- old investigation

--select
--    id,
--    call_path_id,
--    call_time,
--    return_time,
--    elapsed_time,
--    call_id,
--    return_id,
--    flags,
--    syscall_kernel_name
--from syscall_calls 
--where return_time > 4472714303083 and call_time < 4472714622161
--order by call_time
--;

-- let's take 2 cycles of reads and writes
-- 4472714308357 to 4472714316324
-- which is 8us




-- __x64_sys_write is one of the call_path_id for __x64_sys_write
-- looking for some sort of syscall exit function call or branch

--select count(*)
--from call_hierarchy_with_symbols as t1
--where id = 3980
--;

-- 3977 call_path_id is one of the entry_SYSCALL_64s (for the sys_write above)


--select
--    --count(distinct group_id),
--    *
--from call_hierarchy_with_symbols as t1
--where exists (
--    select 1
--    from call_hierarchy_with_symbols as t2
--    where t1.group_id  = t2.group_id
--        and id = 3977
--        and t2.depth > t1.depth
--    )
--order by group_id asc, depth desc
----limit 40
--;

-- the "return" function is called 'syscall_exit_to_user_mode' call_path_id 4052



-- summary:
-- entry_SYSCALL_64 - call_path_id 3977; call id 488029; sample call and return ids 1684912 1685209
-- syscall_exit_to_user_mode - call_path_id 4052; call id 488026; sample call and return ids 1685195 1685204
--
--sqlite> select id, time, dso_short_name, symbol, to_dso_short_name, to_symbol, branch_type_name from samples_view where id between 1685195 and 1685209 order by id;
--id       time           dso_short_name     symbol                             to_dso_short_name  to_symbol                          branch_type_name       
---------  -------------  -----------------  ---------------------------------  -----------------  ---------------------------------  -----------------------
--1685195  4472719142237  [kernel.kallsyms]  do_syscall_64                      [kernel.kallsyms]  syscall_exit_to_user_mode          call                   
--1685196  4472719142237  [kernel.kallsyms]  syscall_exit_to_user_mode          [kernel.kallsyms]  syscall_exit_to_user_mode_prepare  call                   
--1685197  4472719142243  [kernel.kallsyms]  syscall_exit_to_user_mode_prepare  [kernel.kallsyms]  syscall_exit_to_user_mode          return                 
--1685198  4472719142244  [kernel.kallsyms]  syscall_exit_to_user_mode          [kernel.kallsyms]  fpregs_assert_state_consistent     call                   
--1685199  4472719142244  [kernel.kallsyms]  fpregs_assert_state_consistent     [kernel.kallsyms]  fpregs_assert_state_consistent     conditional jump       
--1685200  4472719142244  [kernel.kallsyms]  fpregs_assert_state_consistent     [kernel.kallsyms]  fpregs_assert_state_consistent     unconditional jump     
--1685201  4472719142258  [kernel.kallsyms]  fpregs_assert_state_consistent     [kernel.kallsyms]  syscall_exit_to_user_mode          return                 
--1685202  4472719142259  [kernel.kallsyms]  syscall_exit_to_user_mode          [kernel.kallsyms]  amd_clear_divider                  call                   
--1685203  4472719142273  [kernel.kallsyms]  amd_clear_divider                  [kernel.kallsyms]  syscall_exit_to_user_mode          return                 
--1685204  4472719142275  [kernel.kallsyms]  syscall_exit_to_user_mode          [kernel.kallsyms]  do_syscall_64                      return                 
--1685205  4472719142275  [kernel.kallsyms]  do_syscall_64                      [kernel.kallsyms]  do_syscall_64                      unconditional jump     
--1685206  4472719142278  [kernel.kallsyms]  do_syscall_64                      [kernel.kallsyms]  entry_SYSCALL_64_after_hwframe     return                 
--1685207  4472719142279  [kernel.kallsyms]  syscall_return_via_sysret          [kernel.kallsyms]  syscall_return_via_sysret          unconditional jump     
--1685208  4472719142279  [kernel.kallsyms]  syscall_return_via_sysret          [kernel.kallsyms]  syscall_return_via_sysret          unconditional jump     
--1685209  4472719142642  [kernel.kallsyms]  entry_SYSRETQ_unsafe_stack         libc.so.6          write                              return from system call

-- so the "return_id" of the call is the sample_id of the "return from system call" branch
--
-- the jumps just prior to that look _weird_.  We see a return to
-- entry_SYSCALL_64_after_hwframe instead of entry_SYSCALL_64, and then the
-- symbol magically becomes syscall_return_via_sysret and then
-- entry_SYSRETQ_unsafe_stack all without any jump to those new symbols.  It's
-- possible that it simply executes non-branch instructions past the bounds of
-- the symbol.  That is, there is no return statement/branch within the
-- entry_SYSCALL_64 symbol


-- conclusion:
-- find call_path_ids of interest by looking at frames one above symbol_name =
-- 'x64_sys_call'; find the entry_SYSCALL_64 below this, which should be at a
-- constant depth below it.  The frame above x64_sys_call names which syscall
-- this is.  The entry_SYSCALL_64 links us to calls providing the samples for
-- the call and return.