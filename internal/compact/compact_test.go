package compact

import "testing"

func TestRenderEvent(t *testing.T) {
	cwd := "/tmp/repo/"
	stack := `[[{"file":"/tmp/repo/a.go","function":"A","line":"1"},{"file":"/x/b.go","function":"B","line":"2"},{"file":"c.go","function":"C","line":"3"}],` +
		`[{"file":"/tmp/repo/d.go","function":"D","line":4}],[],[]]`
	tests := []struct{ name, line, want string }{
		{"sibling prefix kept", `{"level":"warn","msg":"m","caller":"/tmp/repo-old/client.go:3"}`,
			`WRN p ?T at=/tmp/repo-old/client.go:3 msg="m"`},
		{"cwd stripped", `{"severity":"error","message":"m","caller":"/tmp/repo/client.go:3","error":"boom"}`,
			`ERR p ?T at=client.go:3 msg="m" error="boom"`},
		{"exact numbers and nesting", `{"severity":"info","message":"m","large":9007199254740993,"obj":{"b": [1, 2]},"time":"x","z":"a\u0007b"}`,
			`INF p ?T msg="m" large=9007199254740993 obj={"b":[1,2]} z="a\ab"`},
		{"stack", `{"severity":"error","message":"m","stack":` + stack + `}`,
			`ERR p ?T msg="m" stack="A a.go:1 > B /x/b.go:2 [+1 frames] | D d.go:4 [+2 groups]"`},
		{"unknown stack shape", `{"severity":"error","message":"m","stack":"plain"}`,
			`ERR p ?T msg="m" stack="plain"`},
		{"null is not an empty string", `{"severity":"warning","message":null,"expected":null,"actual":"","nested":{"v":null}}`,
			`WRN p ?T msg=null actual="" expected=null nested={"v":null}`},
		{"slog source", `{"time":"x","level":"WARN","source":{"function":"F","file":"/tmp/repo/s.go","line":12},"msg":"m","attempt":2}`,
			`WRN p ?T at=s.go:12 msg="m" attempt=2`},
		{"slog level any case", `{"level":"Error","msg":"m"}`,
			`ERR p ?T msg="m"`},
		{"caller wins over source", `{"level":"INFO","msg":"m","caller":"c.go:1","source":{"file":"s.go","line":2}}`,
			`INF p ?T at=c.go:1 msg="m" source={"file":"s.go","line":2}`},
		{"source without a file", `{"level":"WARN","msg":"m","source":{"function":"F"}}`,
			`WRN p ?T msg="m" source={"function":"F"}`},
		{"unknown level", `{"severity":"notice","message":"m","odd key":true}`,
			`NOTICE p ?T msg="m" "odd key"=true`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Parse([]byte(tt.line))
			if e == nil {
				t.Fatal("not recognised")
			}
			if got := e.Render("p", "T", cwd); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
	for _, line := range []string{`{"message":"no level"}`, `{"severity":"info"}`, `{"severity":1,"message":"m"}`, `not json`, `{"severity":"info","message":"m"} trailing`} {
		if Parse([]byte(line)) != nil {
			t.Errorf("%s recognised as an event", line)
		}
	}
}

func TestIsFramework(t *testing.T) {
	for _, line := range []string{"=== RUN   TestA", "    === NAME  TestA/b", "=== PAUSE TestA", "=== CONT  TestA",
		"--- PASS: TestA (0.00s)", "    --- FAIL: TestA/b (0.00s)", "--- SKIP: TestA (0.00s)", "PASS", "FAIL",
		"ok  \tpkg\t0.1s", "FAIL\tpkg\t0.1s", "?   \tpkg\t[no test files]"} {
		if !IsFramework(line) {
			t.Errorf("%q not recognised", line)
		}
	}
	for _, line := range []string{"=== CONFIG: missing service credential", "--- PASSWORD rotated", "PASS: fine", "ok then", "===", "FAILED"} {
		if IsFramework(line) {
			t.Errorf("%q treated as a framework line", line)
		}
	}
}
