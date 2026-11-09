# VPS-native qualification adapter

Preparation only. This branch changes test execution; it does not deploy the
application, install anything on either VPS, or authorize live load.

The coordinator, generator and peer monitor live on the main host. Linux systemd
owns them after the console disconnects. Main-host control uses the existing
allowlist locally. The database host uses the existing restricted, host-key-pinned
test connection without passwords or a Windows/GitHub coordinator. Inter-host SSH
is still required by that retained transport; this does not eliminate network failure.

The generator joins the private owned Docker network at its original trusted
synthetic address172.29.248.21. It never uses host networking or production port8080.
API/worker images are separate immutable release inputs; the old fixture-tool
image is not silently used as the current application image. Running executable
checksums, the restored large fixture, schema108–114, watchdogs and effective
resource limits must be verified before measurement. Published application
configuration is untouched; async fan-out is enabled only inside this test.

Unchanged stage sequence:20 for45s;100 for180s; gradual500 for180s;
sustained500 for1,800s. The original mixed k6 source, generated-client digest,
latency/error/overload gates, generator accounting, write reconciliation, durable
fan-out ledger and delayed mocked notification receipts remain in use. Passing a
ramp is not passing the sustained stage. There is no1,000/10,000/100,000 stage.

The API subgroup retains1CPU/2.5GiB and PostgreSQL2CPU/3GiB. The generator retains
its qualified2CPU/4GiB component ceiling. API, generator and coordinator/peer
processes now share an ADDITIONAL4GiB main-host parent ceiling. They cannot all
consume their separate maxima simultaneously. This is an explicit, more restrictive
shared allocation, not a claim that all component maxima fit independently.
Both host watchdogs/reapers remain outside it. Main admission budgets their full
256MiB plus the unchanged2GiB production reserve and subtracts already resident
test memory when calculating additional demand. The supplied6479MiB available
main-host reading fits this6400MiB initial envelope narrowly; a fresh check is
still required at installation/start. Database7191MiB available fits its3GiB test
ceiling plus reserve and monitors. No swap or production configuration changes.

The no-load Linux proof checks actual nested container cgroup membership and
effective4GiB shared/2.5GiB API limits. Under real load, a shared-parent OOM or
headroom failure is a resource-constrained test outcome, never a successful
qualification or proof of an intrinsic backend limit. CPU/memory/latency/error
and integrity gates remain unchanged.500-session capacity is still unproven.

`lifecycle.py prove` is restricted to disposable Linux CI. It uses real detached
systemd units and sleeping, isolated containers. It tests launcher exit, TERM,
SIGKILL, coordinator deadline and peer loss, with owned cleanup and an unrelated
sentinel. It does not exercise the real VPS database connection, production-health
watchdogs or application workload. Both host-local watchdogs and hard-stop
reapers must still be installed and verified during actual preparation.

The launcher refuses a missing/mismatched lifecycle proof or prepared-runtime
receipt. No runtime receipt is supplied with this branch: neither VPS has been
prepared by this work. In particular, old cleaned-up test directories, credentials
and approvals are not reusable as an active test installation.

After a reviewed package and fresh run configuration are installed on both hosts,
the main-host console command is the following **template, not a command ready
to run against the current servers**:

```bash
python3 /root/atlxpres-cohost/RUN/source/deploy/native-lab/launch.py start /root/atlxpres-cohost/RUN/native.json --approve-trial
python3 /root/atlxpres-cohost/RUN/source/deploy/native-lab/launch.py status /root/atlxpres-cohost/RUN/native.json
```

Use the actual new nonce in place of RUN. `status` reads local units, progress,
final stage results and cleanup confirmation. `stop` invokes systemd teardown.
The coordinator has a120-minute deadline. Teardown preserves evidence, cleans
the database side before removing its main-host key and stops only owned test
resources. An unavailable peer is reported as unconfirmed cleanup; its independent
host reaper remains responsible for stopping its resources. No database rollback,
production container stop, broad prune, purchase or automatic retry is performed.
