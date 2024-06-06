import collections.abc
import duckdb
import pandas


class SqlUtil(object):
    def __init__(self, conn: duckdb.DuckDBPyConnection):
        self.conn = conn

    def execute(self, sql: str) -> pandas.DataFrame:
        self.conn.execute(sql)
        return self.conn.df()

    def execute_script(self, sql: str) -> list[pandas.DataFrame]:
        statements = self.conn.extract_statements(sql)
        return [self.conn.execute(statement) for statement in statements]

    def close(self):
        self.conn.close()
