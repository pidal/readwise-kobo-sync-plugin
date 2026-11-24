"""No external dependencies: tests on generated Kobo-like SQLite fixtures.

Usage: python3 tests/test_integration.py [path-to-native-binary]
"""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import threading
from http.server import HTTPServer, BaseHTTPRequestHandler

BINARY = Path(sys.argv[1] if len(sys.argv)>1 else '/tmp/kobo-rw-test')


def make_db(path, wal=False, many=False):
    con=sqlite3.connect(path)
    con.executescript('''
    CREATE TABLE content (ContentID TEXT NOT NULL, Title TEXT, Attribution TEXT, ContentType INTEGER);
    CREATE TABLE Bookmark (
       BookmarkID TEXT NOT NULL PRIMARY KEY,
       VolumeID TEXT NOT NULL,
       ContentID TEXT NOT NULL,
       Text TEXT,
       Annotation TEXT,
       DateCreated TEXT,
       Hidden BOOL DEFAULT 0
    );
    INSERT INTO content VALUES ('file:///mnt/onboard/Book1.epub','Some Book','An Author',6);
    INSERT INTO content VALUES ('file:///mnt/onboard/Book1.epub#chapter','Should not be selected','x',9);
    INSERT INTO Bookmark VALUES ('id1','file:///mnt/onboard/Book1.epub','ch1','A highlighted passage','Interesting note','2026-01-02T10:11:12Z',0);
    INSERT INTO Bookmark VALUES ('id2','file:///mnt/onboard/Book1.epub','ch1','Deleted highlighted passage',NULL,NULL,'true');
    INSERT INTO Bookmark VALUES ('id3','file:///mnt/onboard/Book1.epub','ch1',NULL,'Only note',NULL,0);
    ''')
    con.commit()
    if wal:
        con.execute('PRAGMA journal_mode=WAL')
        con.execute('PRAGMA wal_autocheckpoint=0')
        con.execute("INSERT INTO Bookmark VALUES ('id4','file:///mnt/onboard/Book1.epub','ch1','In the WAL','hello',NULL,0)")
        con.commit()
    if many:
        for i in range(2000):
            con.execute('INSERT INTO Bookmark VALUES (?, ?, ?, ?, ?, ?, ?)',
                (f'bulk{i}', 'file:///mnt/onboard/Book1.epub', 'ch', f'Long payload {i} '+('✨word '*1400), 'memo', None, 0))
        con.commit()
    return con


class FakeReadwise(BaseHTTPRequestHandler):
    calls=[]
    fail_at=0
    def do_POST(self):
        assert self.path=='/api/v2/highlights/'
        assert self.headers['Authorization']=='Token fake_token_abcdef'
        data=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        self.calls.append(data)
        if self.fail_at and len(self.calls)==self.fail_at:
            self.send_response(401)
            self.end_headers()
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'[]')
    def log_message(self,*args): pass


def invoke(*args):
    return subprocess.run([str(BINARY),*map(str,args)],capture_output=True,text=True)


def run_test():
    with tempfile.TemporaryDirectory() as tmp:
        d=Path(tmp)
        db=d/'KoboReader.sqlite'
        plugin=d/'plugin';plugin.mkdir()
        con=make_db(db,wal=True)
        assert (d/'KoboReader.sqlite-wal').exists()
        ret=invoke('-db',db,'-dir',plugin,'-dry-run')
        assert ret.returncode==0,ret.stderr
        assert 'Found 2 Kobo highlights' in ret.stdout,ret.stdout
        # Go HTTPS requests are exercised with a local mock via Go tests below.
        # A torn/corrupted WAL must fail before exporting potentially bad text.
        wal = Path(str(db) + '-wal')
        original = wal.read_bytes()
        corrupt = bytearray(original)
        corrupt[32 + 24 + 100] ^= 1
        wal.write_bytes(corrupt)
        ret = invoke('-db', db, '-dir', plugin, '-dry-run')
        assert ret.returncode != 0 and 'checksum' in ret.stderr, ret.stderr
        wal.write_bytes(original)
        con.close()

        con2=sqlite3.connect(db)
        con2.execute("INSERT INTO Bookmark VALUES ('id5','file:///mnt/onboard/Book1.epub','ch1',?,NULL,NULL,0)",('✨'*13000,))
        con2.commit()
        ret=invoke('-db',db,'-dir',plugin,'-dry-run')
        assert ret.returncode==0,ret.stderr
        assert 'Found 3 Kobo highlights' in ret.stdout,ret.stdout
        con2.close()

        # Exercise large overflow cells and multi-page B-tree; SQLITE main db.
        manydb=d/'large.sqlite'
        manycon=make_db(manydb,many=True)
        manycon.close()
        ret=invoke('-db',manydb,'-dir',plugin,'-dry-run')
        assert ret.returncode==0,ret.stderr
        assert 'Found 2001 Kobo highlights' in ret.stdout,ret.stdout
        # A normal failed run refreshes a damaged old log without network access.
        (plugin / 'run.log').write_bytes(b'old log' + b'\x00' * 100)
        ret = invoke('-db', db, '-dir', plugin)
        assert ret.returncode != 0  # no token is configured in this test
        log = (plugin / 'run.log').read_bytes()
        assert b'\x00' not in log and b'token missing' in log, log
        assert 'Sync failed' in (plugin / 'last-result.log').read_text()
    print('PASS: SQLite extraction, WAL checksums, large tables and clean error logging')

if __name__=='__main__':run_test()
