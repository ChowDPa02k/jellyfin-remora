package config

import "testing"

func FuzzParseConfiguration(f *testing.F) {
	f.Add([]byte(`config-version: 2
restapi:
  listen: 127.0.0.1
jellyfin:
  path: /opt/jellyfin/jellyfin
  run-as-user: nobody
  data-dir: /srv/jellyfin/data
  config-dir: /srv/jellyfin/config
  cache-dir: /srv/jellyfin/cache
  log-dir: /srv/jellyfin/log
`))
	f.Add([]byte("config-version: 999\nunknown: true\n"))
	f.Add([]byte("&a [*a]\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		cfg, err := Parse(data)
		if err != nil {
			return
		}
		if cfg.ConfigVersion != CurrentVersion {
			t.Fatalf("accepted configuration version %d, want %d", cfg.ConfigVersion, CurrentVersion)
		}
		if cfg.Remora.HeartbeatInterval.Duration <= 0 || cfg.Remora.IOTimeout.Duration <= 0 {
			t.Fatalf("accepted non-positive runtime intervals: %+v", cfg.Remora)
		}
	})
}

func FuzzMigrateConfiguration(f *testing.F) {
	f.Add([]byte("jellyfin:\n  path: /legacy/jellyfin\n"))
	f.Add([]byte("config-version: 1\nremora:\n  heartbeat-interval: 1s\n  health-api-heartbeat: 10\n"))
	f.Add([]byte{0, 1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		migrated, report, err := Migrate(data)
		if err != nil {
			return
		}
		if report.ToVersion != CurrentVersion {
			t.Fatalf("migration ended at version %d", report.ToVersion)
		}
		second, secondReport, err := Migrate(migrated)
		if err != nil || secondReport.FromVersion != CurrentVersion || secondReport.ToVersion != CurrentVersion || len(second) == 0 {
			t.Fatalf("successful migration is not parseable/idempotent: report=%+v err=%v", secondReport, err)
		}
	})
}
