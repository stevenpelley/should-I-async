import collections.abc
import duckdb
import pandas


class SqlUtil(object):
    def __init__(self, conn: duckdb.DuckDBPyConnection):
        self.conn = conn

    def execute(self, sql: str) -> pandas.DataFrame:
        self.conn.execute(sql)
        return self.conn.df()

    def execute_script(self, sql: str) -> collections.abc.Generator[pandas.DataFrame]:
        statements = self.conn.extract_statements(sql)
        for statement in statements:
            self.conn.execute(statement)
            yield self.conn.df()

    def close(self):
        self.conn.close()
