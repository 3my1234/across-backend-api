"""Bounded cleanup of this operator's exact Docker CLI child, not the daemon."""
import json,subprocess,time

def load_archive(archive,log,guard,timeout=120):
    """Use native compressed input; never block the guard loop on a Python pipe."""
    guard()
    process=subprocess.Popen(['docker','load','--input',str(archive)],stdin=subprocess.DEVNULL,stdout=log,stderr=log)
    deadline=time.monotonic()+timeout
    try:
        log.write(json.dumps(dict(import_started=True,cli_pid=process.pid,archive=str(archive)))+'\n');log.flush()
        while process.poll() is None:
            guard()
            if time.monotonic()>=deadline:raise subprocess.TimeoutExpired(process.args,timeout)
            time.sleep(.2)
        guard()
        if process.returncode:raise ValueError('image import failed; inspect image-import.log')
    finally:
        stop_importer(process,log)

def stop_importer(process,log):
    if process.poll() is not None:return True
    log.write(json.dumps(dict(import_cleanup='terminate',cli_pid=process.pid))+'\n');log.flush()
    try:
        process.terminate()
        try:process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            log.write(json.dumps(dict(import_cleanup='kill',cli_pid=process.pid))+'\n');log.flush()
            process.kill()
            process.wait(timeout=3)
        return True
    except Exception as error:
        # Never replace the originating guard/import exception. Retain exact PID
        # for a subsequent operator audit; CLI exit does not prove daemon work ended.
        log.write(json.dumps(dict(import_cleanup='unconfirmed',cli_pid=process.pid,error_class=type(error).__name__))+'\n');log.flush()
        return False
