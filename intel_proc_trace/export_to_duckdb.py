# export-to-sqlite.py: export perf data to a sqlite3 database
# Copyright (c) 2017, Intel Corporation.
#
# This program is free software; you can redistribute it and/or modify it
# under the terms and conditions of the GNU General Public License,
# version 2, as published by the Free Software Foundation.
#
# This program is distributed in the hope it will be useful, but WITHOUT
# ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
# FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
# more details.

from __future__ import print_function

import sys
import struct
import datetime
import duckdb
import pandas

# To use this script you will need to have installed package python-pyside which
# provides LGPL-licensed Python bindings for Qt.  You will also need the package
# libqt4-sql-sqlite for Qt sqlite3 support.
#
# Examples of installing pyside:
#
# ubuntu:
#
#  $ sudo apt-get install python-pyside.qtsql libqt4-sql-psql
#
#  Alternately, to use Python3 and/or pyside 2, one of the following:
#
#    $ sudo apt-get install python3-pyside.qtsql libqt4-sql-psql
#    $ sudo apt-get install python-pyside2.qtsql libqt5sql5-psql
#    $ sudo apt-get install python3-pyside2.qtsql libqt5sql5-psql
# fedora:
#
#  $ sudo yum install python-pyside
#
#  Alternately, to use Python3 and/or pyside 2, one of the following:
#    $ sudo yum install python3-pyside
#    $ pip install --user PySide2
#    $ pip3 install --user PySide2
#
# An example of using this script with Intel PT:
#
#  $ perf record -e intel_pt//u ls
#  $ perf script -s ~/libexec/perf-core/scripts/python/export-to-sqlite.py pt_example branches calls
#  2017-07-31 14:26:07.326913 Creating database...
#  2017-07-31 14:26:07.538097 Writing records...
#  2017-07-31 14:26:09.889292 Adding indexes
#  2017-07-31 14:26:09.958746 Done
#
# To browse the database, sqlite3 can be used e.g.
#
#  $ sqlite3 pt_example
#  sqlite> .header on
#  sqlite> select * from samples_view where id < 10;
#  sqlite> .mode column
#  sqlite> select * from samples_view where id < 10;
#  sqlite> .tables
#  sqlite> .schema samples_view
#  sqlite> .quit
#
# An example of using the database is provided by the script
# exported-sql-viewer.py.  Refer to that script for details.
#
# The database structure is practically the same as created by the script
# export-to-postgresql.py. Refer to that script for details.  A notable
# difference is  the 'transaction' column of the 'samples' table which is
# renamed 'transaction_' in sqlite because 'transaction' is a reserved word.

perf_db_export_mode = True
perf_db_export_calls = False
perf_db_export_callchains = False


def printerr(*args, **keyword_args):
    print(*args, file=sys.stderr, **keyword_args)


def printdate(*args, **kw_args):
    print(datetime.datetime.today(), *args, sep=' ', **kw_args)


def usage():
    printerr(
        "Usage is: export-to-sqlite.py <database name> [<columns>] [<calls>] [<callchains>] [<pyside-version-1>]")
    printerr("where:  columns            'all' or 'branches'")
    printerr("        calls              'calls' => create calls and call_paths table")
    printerr("        callchains         'callchains' => create call_paths table")
    printerr("        pyside-version-1   'pyside-version-1' => use pyside version 1")
    raise Exception("Too few or bad arguments")


if (len(sys.argv) < 2):
    usage()

dbname = sys.argv[1]

if (len(sys.argv) >= 3):
    columns = sys.argv[2]
else:
    columns = "all"

if columns not in ("all", "branches"):
    usage()

branches = (columns == "branches")

for i in range(3, len(sys.argv)):
    if (sys.argv[i] == "calls"):
        perf_db_export_calls = True
    elif (sys.argv[i] == "callchains"):
        perf_db_export_callchains = True
    elif (sys.argv[i] == "pyside-version-1"):
        pass
    else:
        usage()

printdate({
    "columns": columns,
    "branches": branches,
    "perf_db_export_calls": perf_db_export_calls,
    "perf_db_export_callchains": perf_db_export_callchains})

printdate("Creating database ...")

db_exists = False
try:
    f = open(dbname)
    f.close()
    db_exists = True
except:
    pass

if db_exists:
    raise Exception(dbname + " already exists")

con = duckdb.connect(dbname)

con.execute('CREATE TABLE selected_events ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'name    varchar(80))')
con.execute('CREATE TABLE machines ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'pid    integer,'
            'root_dir   varchar(4096))')
con.execute('CREATE TABLE threads ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'machine_id  bigint,'
            'process_id  bigint,'
            'pid    integer,'
            'tid    integer)')
con.execute('CREATE TABLE comms ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'comm    varchar(16),'
            'c_thread_id  bigint,'
            'c_time    bigint,'
            'exec_flag  boolean)')
con.execute('CREATE TABLE comm_threads ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'comm_id  bigint,'
            'thread_id  bigint)')
con.execute('CREATE TABLE dsos ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'machine_id  bigint,'
            'short_name  varchar(256),'
            'long_name  varchar(4096),'
            'build_id  varchar(64))')
con.execute('CREATE TABLE symbols ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'dso_id    bigint,'
            'sym_start  bigint,'
            'sym_end  bigint,'
            'binding  integer,'
            'name    varchar(2048))')
con.execute('CREATE TABLE branch_types ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'name    varchar(80))')

if branches:
    con.execute('CREATE TABLE samples ('
                'id    integer    NOT NULL  PRIMARY KEY,'
                'evsel_id  bigint,'
                'machine_id  bigint,'
                'thread_id  bigint,'
                'comm_id  bigint,'
                'dso_id    bigint,'
                'symbol_id  bigint,'
                'sym_offset  bigint,'
                'ip    bigint,'
                'time    bigint,'
                'cpu    integer,'
                'to_dso_id  bigint,'
                'to_symbol_id  bigint,'
                'to_sym_offset  bigint,'
                'to_ip    bigint,'
                'branch_type  integer,'
                'in_tx    boolean,'
                'call_path_id  bigint,'
                'insn_count  bigint,'
                'cyc_count  bigint,'
                'flags    integer)')
else:
    con.execute('CREATE TABLE samples ('
                'id    integer    NOT NULL  PRIMARY KEY,'
                'evsel_id  bigint,'
                'machine_id  bigint,'
                'thread_id  bigint,'
                'comm_id  bigint,'
                'dso_id    bigint,'
                'symbol_id  bigint,'
                'sym_offset  bigint,'
                'ip    bigint,'
                'time    bigint,'
                'cpu    integer,'
                'to_dso_id  bigint,'
                'to_symbol_id  bigint,'
                'to_sym_offset  bigint,'
                'to_ip    bigint,'
                'period    bigint,'
                'weight    bigint,'
                'transaction_  bigint,'
                'data_src  bigint,'
                'branch_type  integer,'
                'in_tx    boolean,'
                'call_path_id  bigint,'
                'insn_count  bigint,'
                'cyc_count  bigint,'
                'flags    integer)')

if perf_db_export_calls or perf_db_export_callchains:
    con.execute('CREATE TABLE call_paths ('
                'id    integer    NOT NULL  PRIMARY KEY,'
                'parent_id  bigint,'
                'symbol_id  bigint,'
                'ip    bigint)')
if perf_db_export_calls:
    con.execute('CREATE TABLE calls ('
                'id    integer    NOT NULL  PRIMARY KEY,'
                'thread_id  bigint,'
                'comm_id  bigint,'
                'call_path_id  bigint,'
                'call_time  bigint,'
                'return_time  bigint,'
                'branch_count  bigint,'
                'call_id  bigint,'
                'return_id  bigint,'
                'parent_call_path_id  bigint,'
                'flags    integer,'
                'parent_id  bigint,'
                'insn_count  bigint,'
                'cyc_count  bigint)')

con.execute('CREATE TABLE ptwrite ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'payload  bigint,'
            'exact_ip  integer)')

con.execute('CREATE TABLE cbr ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'cbr    integer,'
            'mhz    integer,'
            'percent  integer)')

con.execute('CREATE TABLE mwait ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'hints    integer,'
            'extensions  integer)')

con.execute('CREATE TABLE pwre ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'cstate    integer,'
            'subcstate  integer,'
            'hw    integer)')

con.execute('CREATE TABLE exstop ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'exact_ip  integer)')

con.execute('CREATE TABLE pwrx ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'deepest_cstate  integer,'
            'last_cstate  integer,'
            'wake_reason  integer)')

con.execute('CREATE TABLE context_switches ('
            'id    integer    NOT NULL  PRIMARY KEY,'
            'machine_id  bigint,'
            'time    bigint,'
            'cpu    integer,'
            'thread_out_id  bigint,'
            'comm_out_id  bigint,'
            'thread_in_id  bigint,'
            'comm_in_id  bigint,'
            'flags    integer)')

con.execute('CREATE TABLE auxtrace_errors ('
            'type  integer,'
            'code  integer,'
            'cpu  integer,'
            'pid  integer,'
            'tid  integer,'
            'ip uhugeint,'
            'time bigint,'
            'msg  varchar(100),'
            'cpumode  integer,'
            'machine_pid  integer,'
            'vcpu  integer)')

# printf was added to sqlite in version 3.8.3
sqlite_has_printf = False
try:
    con.execute('SELECT printf("") FROM machines')
    sqlite_has_printf = True
except:
    pass


def emit_to_hex(x):
    if sqlite_has_printf:
        return 'printf("%x", ' + x + ')'
    else:
        return x


con.execute('CREATE VIEW machines_view AS '
            'SELECT '
            'id,'
            'pid,'
            'root_dir,'
            'CASE WHEN id=0 THEN \'unknown\' WHEN pid=-1 THEN \'host\' ELSE \'guest\' END AS host_or_guest'
            ' FROM machines')

con.execute('CREATE VIEW dsos_view AS '
            'SELECT '
            'id,'
            'machine_id,'
            '(SELECT host_or_guest FROM machines_view WHERE id = machine_id) AS host_or_guest,'
            'short_name,'
            'long_name,'
            'build_id'
            ' FROM dsos')

con.execute('CREATE VIEW symbols_view AS '
            'SELECT '
            'id,'
            'name,'
            '(SELECT short_name FROM dsos WHERE id=dso_id) AS dso,'
            'dso_id,'
            'sym_start,'
            'sym_end,'
            'CASE WHEN binding=0 THEN \'local\' WHEN binding=1 THEN \'global\' ELSE \'weak\' END AS binding'
            ' FROM symbols')

con.execute('CREATE VIEW threads_view AS '
            'SELECT '
            'id,'
            'machine_id,'
            '(SELECT host_or_guest FROM machines_view WHERE id = machine_id) AS host_or_guest,'
            'process_id,'
            'pid,'
            'tid'
            ' FROM threads')

con.execute('CREATE VIEW comm_threads_view AS '
            'SELECT '
            'comm_id,'
            '(SELECT comm FROM comms WHERE id = comm_id) AS command,'
            'thread_id,'
            '(SELECT pid FROM threads WHERE id = thread_id) AS pid,'
            '(SELECT tid FROM threads WHERE id = thread_id) AS tid'
            ' FROM comm_threads')

if perf_db_export_calls or perf_db_export_callchains:
    con.execute('CREATE VIEW call_paths_view AS '
                'SELECT '
                'c.id,'
                + emit_to_hex('c.ip') + ' AS ip,'
                'c.symbol_id,'
                '(SELECT name FROM symbols WHERE id = c.symbol_id) AS symbol,'
                '(SELECT dso_id FROM symbols WHERE id = c.symbol_id) AS dso_id,'
                '(SELECT dso FROM symbols_view  WHERE id = c.symbol_id) AS dso_short_name,'
                'c.parent_id,'
                + emit_to_hex('p.ip') + ' AS parent_ip,'
                'p.symbol_id AS parent_symbol_id,'
                '(SELECT name FROM symbols WHERE id = p.symbol_id) AS parent_symbol,'
                '(SELECT dso_id FROM symbols WHERE id = p.symbol_id) AS parent_dso_id,'
                '(SELECT dso FROM symbols_view  WHERE id = p.symbol_id) AS parent_dso_short_name'
                ' FROM call_paths c INNER JOIN call_paths p ON p.id = c.parent_id')
if perf_db_export_calls:
    con.execute('CREATE VIEW calls_view AS '
                'SELECT '
                'calls.id,'
                'thread_id,'
                '(SELECT pid FROM threads WHERE id = thread_id) AS pid,'
                '(SELECT tid FROM threads WHERE id = thread_id) AS tid,'
                '(SELECT comm FROM comms WHERE id = comm_id) AS command,'
                'call_path_id,'
                + emit_to_hex('ip') + ' AS ip,'
                'symbol_id,'
                '(SELECT name FROM symbols WHERE id = symbol_id) AS symbol,'
                'call_time,'
                'return_time,'
                'return_time - call_time AS elapsed_time,'
                'branch_count,'
                'insn_count,'
                'cyc_count,'
                'CASE WHEN cyc_count=0 THEN CAST(0 AS FLOAT) ELSE ROUND(CAST(insn_count AS FLOAT) / cyc_count, 2) END AS IPC,'
                'call_id,'
                'return_id,'
                'CASE WHEN flags=0 THEN \'\' WHEN flags=1 THEN \'no call\' WHEN flags=2 THEN \'no return\' WHEN flags=3 THEN \'no call/return\' WHEN flags=6 THEN \'jump\' ELSE cast(flags as varchar) END AS flags,'
                'parent_call_path_id,'
                'calls.parent_id'
                ' FROM calls INNER JOIN call_paths ON call_paths.id = call_path_id')

con.execute('CREATE VIEW samples_view AS '
            'SELECT '
            'id,'
            'time,'
            'cpu,'
            '(SELECT pid FROM threads WHERE id = thread_id) AS pid,'
            '(SELECT tid FROM threads WHERE id = thread_id) AS tid,'
            '(SELECT comm FROM comms WHERE id = comm_id) AS command,'
            '(SELECT name FROM selected_events WHERE id = evsel_id) AS event,'
            + emit_to_hex('ip') + ' AS ip_hex,'
            '(SELECT name FROM symbols WHERE id = symbol_id) AS symbol,'
            'sym_offset,'
            '(SELECT short_name FROM dsos WHERE id = dso_id) AS dso_short_name,'
            + emit_to_hex('to_ip') + ' AS to_ip_hex,'
            '(SELECT name FROM symbols WHERE id = to_symbol_id) AS to_symbol,'
            'to_sym_offset,'
            '(SELECT short_name FROM dsos WHERE id = to_dso_id) AS to_dso_short_name,'
            '(SELECT name FROM branch_types WHERE id = branch_type) AS branch_type_name,'
            'in_tx,'
            'insn_count,'
            'cyc_count,'
            'CASE WHEN cyc_count=0 THEN CAST(0 AS FLOAT) ELSE ROUND(CAST(insn_count AS FLOAT) / cyc_count, 2) END AS IPC,'
            'flags'
            ' FROM samples')

con.execute('CREATE VIEW ptwrite_view AS '
            'SELECT '
            'ptwrite.id,'
            'time,'
            'cpu,'
            + emit_to_hex('payload') + ' AS payload_hex,'
            'CASE WHEN exact_ip=0 THEN \'False\' ELSE \'True\' END AS exact_ip'
            ' FROM ptwrite'
            ' INNER JOIN samples ON samples.id = ptwrite.id')

con.execute('CREATE VIEW cbr_view AS '
            'SELECT '
            'cbr.id,'
            'time,'
            'cpu,'
            'cbr,'
            'mhz,'
            'percent'
            ' FROM cbr'
            ' INNER JOIN samples ON samples.id = cbr.id')

con.execute('CREATE VIEW mwait_view AS '
            'SELECT '
            'mwait.id,'
            'time,'
            'cpu,'
            + emit_to_hex('hints') + ' AS hints_hex,'
            + emit_to_hex('extensions') + ' AS extensions_hex'
            ' FROM mwait'
            ' INNER JOIN samples ON samples.id = mwait.id')

con.execute('CREATE VIEW pwre_view AS '
            'SELECT '
            'pwre.id,'
            'time,'
            'cpu,'
            'cstate,'
            'subcstate,'
            'CASE WHEN hw=0 THEN \'False\' ELSE \'True\' END AS hw'
            ' FROM pwre'
            ' INNER JOIN samples ON samples.id = pwre.id')

con.execute('CREATE VIEW exstop_view AS '
            'SELECT '
            'exstop.id,'
            'time,'
            'cpu,'
            'CASE WHEN exact_ip=0 THEN \'False\' ELSE \'True\' END AS exact_ip'
            ' FROM exstop'
            ' INNER JOIN samples ON samples.id = exstop.id')

con.execute('CREATE VIEW pwrx_view AS '
            'SELECT '
            'pwrx.id,'
            'time,'
            'cpu,'
            'deepest_cstate,'
            'last_cstate,'
            'CASE     WHEN wake_reason=1 THEN \'Interrupt\''
            ' WHEN wake_reason=2 THEN \'Timer Deadline\''
            ' WHEN wake_reason=4 THEN \'Monitored Address\''
            ' WHEN wake_reason=8 THEN \'HW\''
            ' ELSE cast(wake_reason as varchar) '
            'END AS wake_reason'
            ' FROM pwrx'
            ' INNER JOIN samples ON samples.id = pwrx.id')

con.execute('CREATE VIEW power_events_view AS '
            'SELECT '
            'samples.id,'
            'time,'
            'cpu,'
            'selected_events.name AS event,'
            'CASE WHEN selected_events.name=\'cbr\' THEN (SELECT cbr FROM cbr WHERE cbr.id = samples.id) ELSE \'\' END AS cbr,'
            'CASE WHEN selected_events.name=\'cbr\' THEN (SELECT mhz FROM cbr WHERE cbr.id = samples.id) ELSE \'\' END AS mhz,'
            'CASE WHEN selected_events.name=\'cbr\' THEN (SELECT percent FROM cbr WHERE cbr.id = samples.id) ELSE \'\' END AS percent,'
            'CASE WHEN selected_events.name=\'mwait\' THEN (SELECT ' + emit_to_hex(
                'hints') + ' FROM mwait WHERE mwait.id = samples.id) ELSE \'\' END AS hints_hex,'
            'CASE WHEN selected_events.name=\'mwait\' THEN (SELECT ' + emit_to_hex(
                'extensions') + ' FROM mwait WHERE mwait.id = samples.id) ELSE \'\' END AS extensions_hex,'
            'CASE WHEN selected_events.name=\'pwre\' THEN (SELECT cstate FROM pwre WHERE pwre.id = samples.id) ELSE \'\' END AS cstate,'
            'CASE WHEN selected_events.name=\'pwre\' THEN (SELECT subcstate FROM pwre WHERE pwre.id = samples.id) ELSE \'\' END AS subcstate,'
            'CASE WHEN selected_events.name=\'pwre\' THEN (SELECT hw FROM pwre WHERE pwre.id = samples.id) ELSE \'\' END AS hw,'
            'CASE WHEN selected_events.name=\'exstop\' THEN (SELECT exact_ip FROM exstop WHERE exstop.id = samples.id) ELSE \'\' END AS exact_ip,'
            'CASE WHEN selected_events.name=\'pwrx\' THEN (SELECT deepest_cstate FROM pwrx WHERE pwrx.id = samples.id) ELSE \'\' END AS deepest_cstate,'
            'CASE WHEN selected_events.name=\'pwrx\' THEN (SELECT last_cstate FROM pwrx WHERE pwrx.id = samples.id) ELSE \'\' END AS last_cstate,'
            'CASE WHEN selected_events.name=\'pwrx\' THEN (SELECT '
            'CASE     WHEN wake_reason=1 THEN \'Interrupt\''
            ' WHEN wake_reason=2 THEN \'Timer Deadline\''
            ' WHEN wake_reason=4 THEN \'Monitored Address\''
            ' WHEN wake_reason=8 THEN \'HW\''
            ' ELSE cast(wake_reason as varchar) '
            'END'
            ' FROM pwrx WHERE pwrx.id = samples.id) ELSE \'\' END AS wake_reason'
            ' FROM samples'
            ' INNER JOIN selected_events ON selected_events.id = evsel_id'
            ' WHERE selected_events.name IN (\'cbr\',\'mwait\',\'exstop\',\'pwre\',\'pwrx\')')

con.execute('CREATE VIEW context_switches_view AS '
            'SELECT '
            'context_switches.id,'
            'context_switches.machine_id,'
            'context_switches.time,'
            'context_switches.cpu,'
            'th_out.pid AS pid_out,'
            'th_out.tid AS tid_out,'
            'comm_out.comm AS comm_out,'
            'th_in.pid AS pid_in,'
            'th_in.tid AS tid_in,'
            'comm_in.comm AS comm_in,'
            'CASE    WHEN context_switches.flags = 0 THEN \'in\''
            ' WHEN context_switches.flags = 1 THEN \'out\''
            ' WHEN context_switches.flags = 3 THEN \'out preempt\''
            ' ELSE cast(context_switches.flags as varchar) '
            'END AS flags'
            ' FROM context_switches'
            ' INNER JOIN threads AS th_out ON th_out.id   = context_switches.thread_out_id'
            ' INNER JOIN threads AS th_in  ON th_in.id    = context_switches.thread_in_id'
            ' INNER JOIN comms AS comm_out ON comm_out.id = context_switches.comm_out_id'
            ' INNER JOIN comms AS comm_in  ON comm_in.id  = context_switches.comm_in_id')

con.execute('CREATE VIEW auxtrace_errors_view AS '
            'SELECT '
            'ROWID as id,'
            'time,'
            'cpu,'
            'pid,'
            'tid,'
            'CASE WHEN type = 1 THEN \'ITRACE\''
            ' WHEN type = 2 THEN \'MAX\''
            ' ELSE cast(type as varchar) '
            'END AS type,'
            'CASE WHEN code = 1 THEN \'NOMEM\''
            ' WHEN code = 2 THEN \'INTERN\''
            ' WHEN code = 3 THEN \'BADPKT\''
            ' WHEN code = 4 THEN \'NODATA\''
            ' WHEN code = 5 THEN \'NOINSN\''
            ' WHEN code = 6 THEN \'MISMAT\''
            ' WHEN code = 7 THEN \'OVR\''
            ' WHEN code = 8 THEN \'LOST\''
            ' WHEN code = 9 THEN \'UNK\''
            ' WHEN code = 10 THEN \'NELOOP\''
            ' WHEN code = 11 THEN \'EPTW\''
            ' WHEN code = 12 THEN \'MAX\''
            ' ELSE cast(code as varchar) '
            'END AS code,'
            + emit_to_hex('ip') + ' AS ip_hex,'
            'msg,'
            'cpumode,'
            'machine_pid,'
            'vcpu'
            ' FROM auxtrace_errors')

evsel_query = "INSERT INTO selected_events VALUES (?, ?)"
machine_query = "INSERT INTO machines VALUES (?, ?, ?)"
thread_query = "INSERT INTO threads VALUES (?, ?, ?, ?, ?)"
comm_query = "INSERT INTO comms VALUES (?, ?, ?, ?, ?)"
comm_thread_query = "INSERT INTO comm_threads VALUES (?, ?, ?)"
dso_query = "INSERT INTO dsos VALUES (?, ?, ?, ?, ?)"
symbol_query = "INSERT INTO symbols VALUES (?, ?, ?, ?, ?, ?)"
branch_type_query = "INSERT INTO branch_types VALUES (?, ?)"
if branches:
    sample_query = "INSERT INTO samples VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
else:
    sample_query = "INSERT INTO samples VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
if perf_db_export_calls or perf_db_export_callchains:
    call_path_query = "INSERT INTO call_paths VALUES (?, ?, ?, ?)"
if perf_db_export_calls:
    call_query = "INSERT INTO calls VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
ptwrite_query = "INSERT INTO ptwrite VALUES (?, ?, ?)"
cbr_query = "INSERT INTO cbr VALUES (?, ?, ?, ?)"
mwait_query = "INSERT INTO mwait VALUES (?, ?, ?)"
pwre_query = "INSERT INTO pwre VALUES (?, ?, ?, ?)"
exstop_query = "INSERT INTO exstop VALUES (?, ?)"
pwrx_query = "INSERT INTO pwrx VALUES (?, ?, ?, ?)"
context_switch_query = "INSERT INTO context_switches VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"
auxtrace_error_query = "INSERT INTO auxtrace_errors VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"


def trace_begin():
    printdate("Collecting records...")
    # id == 0 means unknown.  It is easier to create records for them than replace the zeroes with NULLs
    evsel_table(0, "unknown")
    machine_table(0, 0, "unknown")
    thread_table(0, 0, 0, -1, -1)
    comm_table(0, "unknown", 0, 0, 0)
    dso_table(0, 0, "unknown", "unknown", "")
    symbol_table(0, 0, 0, 0, 0, "unknown")
    sample_table(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
                 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
    if perf_db_export_calls or perf_db_export_callchains:
        call_path_table(0, 0, 0, 0)
        call_return_table(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)


unhandled_count = 0


def is_table_empty(table_name):
    con.execute('SELECT * FROM ' + table_name + ' LIMIT 1')
    result = con.fetchall()
    return len(result) == 0


def drop(table_name):
    con.execute('DROP VIEW ' + table_name + '_view')
    con.execute('DROP TABLE ' + table_name)


def insert_data(table_name, data):
    if len(data) == 0:
        return
    pandas_df = pandas.DataFrame(data)
    con.execute('INSERT INTO {} SELECT * FROM pandas_df'.format(table_name))


def trace_end():
    printdate("Inserting data")
    insert_data('selected_events', evsel_data)
    insert_data('machines', machine_data)
    insert_data('threads', thread_data)
    insert_data('comms', comm_data)
    insert_data('comm_threads', comm_thread_data)
    insert_data('dsos', dso_data)
    insert_data('symbols', symbol_data)
    insert_data('branch_types', branch_type_data)
    insert_data('samples', sample_data)
    insert_data('call_paths', call_path_data)
    insert_data('calls', call_data)
    insert_data('ptwrite', ptwrite_data)
    insert_data('cbr', cbr_data)
    insert_data('mwait', mwait_data)
    insert_data('pwre', pwre_data)
    insert_data('exstop', exstop_data)
    insert_data('pwrx', pwrx_data)
    insert_data('context_switches',  context_switch_data)
    insert_data('auxtrace_errors',  auxtrace_error_data)

    printdate("Dropping unused tables")
    if is_table_empty("ptwrite"):
        drop("ptwrite")
    if is_table_empty("mwait") and is_table_empty("pwre") and is_table_empty("exstop") and is_table_empty("pwrx"):
        con.execute('DROP VIEW power_events_view')
        drop("mwait")
        drop("pwre")
        drop("exstop")
        drop("pwrx")
        if is_table_empty("cbr"):
            drop("cbr")
    if is_table_empty("context_switches"):
        drop("context_switches")
    if is_table_empty("auxtrace_errors"):
        drop("auxtrace_errors")

    if (unhandled_count):
        printdate("Warning: ", unhandled_count, " unhandled events")
    printdate("Done")
    con.close()


def trace_unhandled(event_name, context, event_fields_dict):
    global unhandled_count
    unhandled_count += 1


def sched__sched_switch(*x):
    pass


evsel_data = []


def evsel_table(*x):
    evsel_data.append(x)


machine_data = []


def machine_table(*x):
    machine_data.append(x)


thread_data = []


def thread_table(*x):
    thread_data.append(x)


comm_data = []


def comm_table(*x):
    comm_data.append(x)


comm_thread_data = []


def comm_thread_table(*x):
    comm_thread_data.append(x)


dso_data = []


def dso_table(*x):
    dso_data.append(x)


symbol_data = []


def symbol_table(*x):
    symbol_data.append(x)


branch_type_data = []


def branch_type_table(*x):
    branch_type_data.append(x)


sample_data = []


def sample_table(*x):
    xx = x if not branches else x[0:15] + x[19:25]
    sample_data.append(xx)


call_path_data = []


def call_path_table(*x):
    call_path_data.append(x)


call_data = []


def call_return_table(*x):
    call_data.append(x)


ptwrite_data = []


def ptwrite(id, raw_buf):
    data = struct.unpack_from("<IQ", raw_buf)
    flags = data[0]
    payload = data[1]
    exact_ip = flags & 1
    x = (str(id), str(payload), str(exact_ip))
    call_data.append(x)


cbr_data = []


def cbr(id, raw_buf):
    data = struct.unpack_from("<BBBBII", raw_buf)
    cbr = data[0]
    MHz = (data[4] + 500) / 1000
    percent = ((cbr * 1000 / data[2]) + 5) / 10
    x = (str(id), str(cbr), str(MHz), str(percent))
    cbr_data.append(x)


mwait_data = []


def mwait(id, raw_buf):
    data = struct.unpack_from("<IQ", raw_buf)
    payload = data[1]
    hints = payload & 0xff
    extensions = (payload >> 32) & 0x3
    x = (str(id), str(hints), str(extensions))
    mwait_data.append(x)


pwre_data = []


def pwre(id, raw_buf):
    data = struct.unpack_from("<IQ", raw_buf)
    payload = data[1]
    hw = (payload >> 7) & 1
    cstate = (payload >> 12) & 0xf
    subcstate = (payload >> 8) & 0xf
    x = (str(id), str(cstate), str(subcstate), str(hw))
    pwre_data.append(x)


exstop_data = []


def exstop(id, raw_buf):
    data = struct.unpack_from("<I", raw_buf)
    flags = data[0]
    exact_ip = flags & 1
    x = (str(id), str(exact_ip))
    exstop_data.append(x)


pwrx_data = []


def pwrx(id, raw_buf):
    data = struct.unpack_from("<IQ", raw_buf)
    payload = data[1]
    deepest_cstate = payload & 0xf
    last_cstate = (payload >> 4) & 0xf
    wake_reason = (payload >> 8) & 0xf
    x = (str(id), str(deepest_cstate), str(last_cstate), str(wake_reason))
    pwrx_data.append(x)


def synth_data(id, config, raw_buf, *x):
    if config == 0:
        ptwrite(id, raw_buf)
    elif config == 1:
        mwait(id, raw_buf)
    elif config == 2:
        pwre(id, raw_buf)
    elif config == 3:
        exstop(id, raw_buf)
    elif config == 4:
        pwrx(id, raw_buf)
    elif config == 5:
        cbr(id, raw_buf)


context_switch_data = []


def context_switch_table(*x):
    context_switch_data.append(x)


auxtrace_error_data = []


def auxtrace_error(*x):
    # args defined in kernel/tools/perf/util/scripting-engines/trace-event-python.c:python_process_auxtrace_error()
    # t = tuple_new(11);
    #
    # tuple_set_u32(t, 0, e->type);
    # tuple_set_u32(t, 1, e->code);
    # tuple_set_s32(t, 2, e->cpu);
    # tuple_set_s32(t, 3, e->pid);
    # tuple_set_s32(t, 4, e->tid);
    # tuple_set_u64(t, 5, e->ip);
    # tuple_set_u64(t, 6, tm);
    # tuple_set_string(t, 7, msg);
    # tuple_set_u32(t, 8, cpumode);
    # tuple_set_s32(t, 9, e->machine_pid);
    # tuple_set_s32(t, 10, e->vcpu);
    #
    #
    # typ comes from kernel/tools/perf/util/auxtrace.h enum PERF_AUXTRACE_ERR_* and is expected to be 1
    # enum auxtrace_error_type {
    #     PERF_AUXTRACE_ERROR_ITRACE  = 1,
    #     PERF_AUXTRACE_ERROR_MAX
    # };
    #
    # code comes from kernel/tools/perf/util/intel-pt-decoder/intel-pt-decoder.h enum INTEL_PT_ERR_* with 7 being OVR for overflow
    #
    # enum {
    #   INTEL_PT_ERR_NOMEM = 1,
    #   INTEL_PT_ERR_INTERN,
    #   INTEL_PT_ERR_BADPKT,
    #   INTEL_PT_ERR_NODATA,
    #   INTEL_PT_ERR_NOINSN,
    #   INTEL_PT_ERR_MISMAT,
    #   INTEL_PT_ERR_OVR,
    #   INTEL_PT_ERR_LOST,
    #   INTEL_PT_ERR_UNK,
    #   INTEL_PT_ERR_NELOOP,
    #   INTEL_PT_ERR_EPTW,
    #   INTEL_PT_ERR_MAX,
    # };
    try:
        auxtrace_error_data.append(x)
    except Exception as e:
        print("error {}, x {}".format(e, x))
        raise e
