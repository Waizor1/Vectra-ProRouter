import os, pathlib, subprocess, tempfile, unittest
SCRIPT=pathlib.Path(__file__).with_name('backup-postgres.sh').resolve()
class BackupTests(unittest.TestCase):
 def run_backup(self, mode='ok', unsafe=None):
  with tempfile.TemporaryDirectory() as directory:
   root=pathlib.Path(directory).resolve(); tools=root/'bin';tools.mkdir()
   dump=tools/'pg_dump';dump.write_text('#!/bin/sh\nprintf synthetic-only\n'+('exit 7\n' if mode=='dumpfail' else ''));dump.chmod(0o700)
   if mode=='gzipfail':
    gzip=tools/'gzip';gzip.write_text('#!/bin/sh\nexit 8\n');gzip.chmod(0o700)
   backup=root/'backup'
   if unsafe=='perms': backup.mkdir(mode=0o755)
   if unsafe=='symlink': backup.symlink_to(tools,target_is_directory=True)
   env={**os.environ,'PATH':str(tools)+':'+os.environ['PATH'],'POSTGRES_HOST':'fake','POSTGRES_PORT':'1','POSTGRES_DB':'fake','POSTGRES_USER':'fake','BACKUP_DIR':str(backup),'BACKUP_KEEP_DAYS':'14'}
   if unsafe=='relative':env['BACKUP_DIR']='relative'
   if unsafe=='retention':env['BACKUP_KEEP_DAYS']='1 -delete'
   result=subprocess.run(['sh',str(SCRIPT)],env=env,capture_output=True,text=True)
   if mode=='ok' and unsafe is None:
    self.assertEqual(result.returncode,0,result.stderr);self.assertEqual(backup.stat().st_mode&0o777,0o700)
    files=list(backup.glob('*.gz'));self.assertEqual(len(files),1);self.assertEqual(files[0].stat().st_mode&0o777,0o600)
   else:
    self.assertNotEqual(result.returncode,0);self.assertNotIn('Backup completed:',result.stdout)
    if backup.exists() and not backup.is_symlink():self.assertEqual(list(backup.glob('*.gz')),[]);self.assertEqual(list(backup.glob('.vectra-backup.*')),[])
    if unsafe=='perms':self.assertEqual(backup.stat().st_mode&0o777,0o755)
 def test_success(self):self.run_backup()
 def test_dumpfail(self):self.run_backup('dumpfail')
 def test_gzipfail(self):self.run_backup('gzipfail')
 def test_paths(self):
  for unsafe in ['perms','symlink','relative','retention']:
   with self.subTest(unsafe=unsafe):self.run_backup(unsafe=unsafe)
if __name__=='__main__':unittest.main()
