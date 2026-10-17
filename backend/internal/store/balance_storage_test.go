package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"returnsrecon/internal/model"
)

func legacyBalanceTable(t *testing.T, s *Store, extra string) {
	t.Helper()
	var definition string
	if err := s.DB.QueryRow("SELECT sql FROM sqlite_schema WHERE name='sale_balances'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	definition = strings.TrimSuffix(definition, " WITHOUT ROWID")
	if extra != "" {
		definition = strings.TrimSuffix(definition, ")") + ", " + extra + ")"
	}
	if _, err := s.DB.Exec("DROP TABLE sale_balances; " + definition); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyLegacyBalanceLayoutChangesOnlyInsideRun(t *testing.T) {
	for _, kind := range []string{"plain", "trigger", "index", "constraint"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.sqlite")
			s := openTest(t, path)
			extra := ""
			if kind == "constraint" {
				extra = "CHECK(remaining<=original)"
			}
			legacyBalanceTable(t, s, extra)
			custom := ""
			switch kind {
			case "trigger":
				custom = "CREATE TRIGGER keep_balance_layout AFTER INSERT ON sale_balances BEGIN SELECT 1; END"
			case "index":
				custom = "CREATE INDEX keep_balance_layout ON sale_balances(sale_id)"
			}
			if custom != "" {
				if _, err := s.DB.Exec(custom); err != nil {
					t.Fatal(err)
				}
			}
			dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 6, "09:00"), fixtureRow("0:3", 3, -4, "10:00")})
			if _, err := s.Import(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if scalar(t, opened, "SELECT wr FROM pragma_table_list WHERE name='sale_balances'") != 0 {
				t.Fatal("Open migrated the legacy table outside Run")
			}
			opened.Close()
			run, err := s.Run(context.Background(), m.DatasetID, 2)
			if err != nil || run.Matched != 1 {
				t.Fatalf("legacy run: %+v %v", run, err)
			}
			want := 0
			if kind == "plain" {
				want = 1
			}
			if scalar(t, s, "SELECT wr FROM pragma_table_list WHERE name='sale_balances'") != want || scalar(t, s, "SELECT count(*) FROM pragma_foreign_key_check") != 0 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE run_id=?", run.RunID) != 2 {
				t.Fatal("layout, foreign keys, or complete balance changed unexpectedly")
			}
			if custom != "" {
				if scalar(t, s, "SELECT count(*) FROM sqlite_schema WHERE name='keep_balance_layout'") != 1 {
					t.Fatal("custom schema was removed")
				}
				if kind == "trigger" {
					if _, err = s.DB.Exec("DROP TRIGGER keep_balance_layout"); err != nil {
						t.Fatal(err)
					}
					if err = s.ensureBalanceStorage(context.Background()); err != nil || scalar(t, s, "SELECT wr FROM pragma_table_list WHERE name='sale_balances'") != 0 || scalar(t, s, "SELECT remaining FROM sale_balances WHERE run_id=?", run.RunID) != 2 {
						t.Fatal("nonempty legacy history was migrated or changed")
					}
				}
			}
		})
	}
}

func TestSparseConservationRejectsBothMissingAndPhantomAllocationBalances(t *testing.T) {
	for _, corruption := range []string{
		"UPDATE sale_balances SET allocated=0,remaining=original WHERE sale_id='0:2' AND run_id=NEW.run_id",
		"UPDATE sale_balances SET allocated=1,remaining=original-1 WHERE sale_id='0:3' AND run_id=NEW.run_id",
	} {
		t.Run(corruption, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "relation.sqlite"))
			ordinary := fixtureRow("0:3", 3, 7, "09:00")
			ordinary.Customer = "ordinary"
			dir, m := preparedFixture(t, []model.Row{fixtureRow("0:2", 2, 6, "09:00"), ordinary, fixtureRow("0:4", 4, -4, "10:00")})
			if _, err := s.Import(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec("CREATE TRIGGER corrupt_allocation_balance AFTER INSERT ON allocations BEGIN " + corruption + "; END"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Run(context.Background(), m.DatasetID, 2); err == nil || scalar(t, s, "SELECT count(*) FROM runs WHERE status='published'") != 0 {
				t.Fatal("corrupt allocation relation was published")
			}
		})
	}
}
