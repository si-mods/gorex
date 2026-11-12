package rex

import "testing"

func TestProgramName(t *testing.T) {
	for _, tt := range []struct {
		label, name string
		args        []string
		want        string
	}{
		{label: "version with command", name: "2.1.312", args: []string{"claude", "--resume"}, want: "claude"},
		{label: "version with path", name: "2.1.312", args: []string{"/Users/x/.local/bin/claude"}, want: "claude"},
		{label: "version without args", name: "2.1.312", want: "2.1.312"},
		{label: "p_comm truncation", name: "verylongprogram", args: []string{"/bin/verylongprogramname"}, want: "verylongprogramname"},
		{label: "image renamed by its update", name: "omp.exe.1794363945117.6604.0.bak", args: []string{"/home/x/.bun/bin/omp.exe"}, want: "omp"},
		{label: "arguments name the renamed image", name: "omp.exe.1794363945117.6604.0.bak", args: []string{"/home/x/.bun/bin/omp.exe.1794363945117.6604.0.bak"}, want: "omp"},
		{label: "image renamed, arguments unread", name: "OMP.EXE.1794363945117.6604.0.bak", want: "OMP"},
		{label: "nothing before the extension", name: ".exe.1.2.3.bak", want: ".exe.1.2.3.bak"},
		{label: "node script", name: "node", args: []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js"}, want: "codex"},
		{label: "login shell", name: "zsh", args: []string{"-zsh"}, want: "zsh"},
	} {
		t.Run(tt.label, func(t *testing.T) {
			if got := (procInfo{name: tt.name, args: tt.args}).programName(); got != tt.want {
				t.Errorf("programName() = %q, want %q", got, tt.want)
			}
		})
	}
}
