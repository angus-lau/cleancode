package schema

import (
	"testing"
)

func testSchema() *DBSchema {
	return &DBSchema{
		Tables: []Table{
			{
				Name: "posts",
				Columns: []Column{
					{Name: "id"},
					{Name: "user_id"},
					{Name: "token_supply"},
					{Name: "media"},
					{Name: "hashtag"},
					{Name: "created_at"},
				},
			},
			{
				Name: "users",
				Columns: []Column{
					{Name: "id"},
					{Name: "username"},
					{Name: "aura"},
				},
			},
		},
	}
}

func TestValidateDiff_SQLQualifiedColumn(t *testing.T) {
	diff := `diff --git a/src/service.ts b/src/service.ts
--- a/src/service.ts
+++ b/src/service.ts
@@ -10,6 +10,7 @@
     const data = await sqlAdmin` + "`" + `
       SELECT p.id, p.aura, p.media
       FROM posts p
+      WHERE p.aura > 0
     ` + "`" + `;`

	findings := ValidateDiff(diff, testSchema())

	if len(findings) == 0 {
		t.Fatal("expected findings for p.aura, got none")
	}

	found := false
	for _, f := range findings {
		if f.Column == "aura" && f.Table == "posts" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected finding for column 'aura' on table 'posts', got: %+v", findings)
	}
}

func TestValidateDiff_ValidColumn(t *testing.T) {
	diff := `diff --git a/src/service.ts b/src/service.ts
--- a/src/service.ts
+++ b/src/service.ts
@@ -10,6 +10,7 @@
     const data = await sqlAdmin` + "`" + `
       SELECT p.id, p.token_supply
       FROM posts p
+      WHERE p.token_supply > 0
     ` + "`" + `;`

	findings := ValidateDiff(diff, testSchema())

	if len(findings) != 0 {
		t.Errorf("expected no findings for valid columns, got: %+v", findings)
	}
}

func TestValidateDiff_SupabaseSelect(t *testing.T) {
	diff := `diff --git a/src/controller.ts b/src/controller.ts
--- a/src/controller.ts
+++ b/src/controller.ts
@@ -5,6 +5,8 @@
+    const { data } = await supabase
+      .from("posts")
+      .select("id, aura, media")
+      .order("aura", { ascending: false });`

	findings := ValidateDiff(diff, testSchema())

	if len(findings) == 0 {
		t.Fatal("expected findings for 'aura' in .select(), got none")
	}

	// Should find aura in both .select() and .order()
	selectFound := false
	orderFound := false
	for _, f := range findings {
		if f.Column == "aura" && f.Table == "posts" {
			if f.Message != "" {
				if contains(f.Message, ".select()") {
					selectFound = true
				}
				if contains(f.Message, ".order()") {
					orderFound = true
				}
			}
		}
	}
	if !selectFound {
		t.Error("expected finding for 'aura' in .select() call")
	}
	if !orderFound {
		t.Error("expected finding for 'aura' in .order() call")
	}
}

func TestValidateDiff_UsersAuraNotFlagged_WhenDirect(t *testing.T) {
	// users.aura is a valid column and should NOT be flagged
	diff := `diff --git a/src/service.ts b/src/service.ts
--- a/src/service.ts
+++ b/src/service.ts
@@ -10,6 +10,7 @@
+    SELECT u.aura FROM users u`

	findings := ValidateDiff(diff, testSchema())

	for _, f := range findings {
		if f.Column == "aura" && f.Table == "users" {
			t.Errorf("should not flag users.aura (valid column), got: %+v", f)
		}
	}
}

func TestValidateDiff_SimilarColumnSuggestion(t *testing.T) {
	diff := `diff --git a/src/service.ts b/src/service.ts
--- a/src/service.ts
+++ b/src/service.ts
@@ -10,6 +10,7 @@
+    SELECT p.toekn_supply FROM posts p`

	findings := ValidateDiff(diff, testSchema())

	if len(findings) == 0 {
		t.Fatal("expected finding for typo 'toekn_supply'")
	}

	if findings[0].Suggestion == "" {
		t.Error("expected a suggestion for similar column name")
	}
}

func TestValidateDiff_MethodCallNotFlagged(t *testing.T) {
	// posts.map(...) is a JS method call, not a SQL column reference
	diff := `diff --git a/src/service.ts b/src/service.ts
--- a/src/service.ts
+++ b/src/service.ts
@@ -10,6 +10,7 @@
+    const results = posts.map((p) => p.id);
+    const filtered = posts.filter((p) => p.token_supply > 0);`

	findings := ValidateDiff(diff, testSchema())

	for _, f := range findings {
		if f.Column == "map" || f.Column == "filter" {
			t.Errorf("should not flag method calls as column references, got: %+v", f)
		}
	}
}

func TestParseSupabaseSelectCols(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{"id, aura, media", []string{"id", "aura", "media"}},
		{"id, posts!inner(id, media), aura", []string{"id", "aura"}},
		{"id, token_supply", []string{"id", "token_supply"}},
		{"id", []string{"id"}},
	}

	for _, tc := range cases {
		got := parseSupabaseSelectCols(tc.input)
		if len(got) != len(tc.expected) {
			t.Errorf("parseSupabaseSelectCols(%q) = %v, want %v", tc.input, got, tc.expected)
			continue
		}
		for i, col := range got {
			if col != tc.expected[i] {
				t.Errorf("parseSupabaseSelectCols(%q)[%d] = %q, want %q", tc.input, i, col, tc.expected[i])
			}
		}
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b     string
		expected int
	}{
		{"aura", "aura", 0},
		{"aura", "aure", 1},
		{"aura", "token_supply", 11},
		{"toekn_supply", "token_supply", 2},
	}

	for _, tc := range cases {
		got := levenshtein(tc.a, tc.b)
		if got != tc.expected {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.expected)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
