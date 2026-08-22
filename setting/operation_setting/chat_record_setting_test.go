package operation_setting

import "testing"

// The page collects parts; what reaches pgx has to be a valid DSN with the
// password escaped, whatever the operator typed into the password box.
func TestResolvedDSNBuildsFromParts(t *testing.T) {
	setting := ChatRecordSetting{
		Host: "10.0.0.5", Port: "5433", Database: "chatlog",
		User: "logger", Password: "p@ss/w:rd?", SSLMode: "require",
	}
	got := setting.ResolvedDSN()
	want := "postgres://logger:p%40ss%2Fw%3Ard%3F@10.0.0.5:5433/chatlog?sslmode=require"
	if got != want {
		t.Fatalf("ResolvedDSN() = %q, want %q", got, want)
	}
}

func TestResolvedDSNFillsInDefaults(t *testing.T) {
	setting := ChatRecordSetting{Host: "db", Database: "chatlog", User: "logger"}
	got := setting.ResolvedDSN()
	want := "postgres://logger@db:5432/chatlog?sslmode=disable"
	if got != want {
		t.Fatalf("ResolvedDSN() = %q, want %q", got, want)
	}
}

// An installation configured with the older single-string form keeps working.
func TestResolvedDSNFallsBackToTheOlderStringForm(t *testing.T) {
	setting := ChatRecordSetting{DSN: "postgres://u:p@host:5432/db?sslmode=disable"}
	if got := setting.ResolvedDSN(); got != setting.DSN {
		t.Fatalf("ResolvedDSN() = %q, want the saved DSN", got)
	}

	// And the parts win once they are filled in.
	setting.Host = "newhost"
	setting.Database = "newdb"
	setting.User = "newuser"
	if got := setting.ResolvedDSN(); got == setting.DSN {
		t.Fatal("the older string form overrode the parts")
	}
}

func TestResolvedDSNIsEmptyWhenNothingIsConfigured(t *testing.T) {
	var setting ChatRecordSetting
	if got := setting.ResolvedDSN(); got != "" {
		t.Fatalf("ResolvedDSN() = %q, want empty", got)
	}
}

// Describe is what the settings page shows back; it must never carry the
// password.
func TestDescribeOmitsThePassword(t *testing.T) {
	setting := ChatRecordSetting{
		Host: "10.0.0.5", Port: "5433", Database: "chatlog",
		User: "logger", Password: "s3cret",
	}
	got := setting.Describe()
	if got != "logger@10.0.0.5:5433/chatlog" {
		t.Fatalf("Describe() = %q", got)
	}
}
