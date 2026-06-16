package approval

import "testing"

func TestIsReadOnly(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		// --- 已知只读命令 ---
		{"ls", "ls", true},
		{"ls -la", "ls -la", true},
		{"cat file.txt", "cat file.txt", true},
		{"pwd", "pwd", true},
		{"echo hello", "echo hello", true},
		{"grep pattern file.txt", "grep pattern file.txt", true},
		{"find . -name '*.go'", "find . -name '*.go'", true},
		{"head -n 10 file", "head -n 10 file", true},
		{"tail -f log", "tail -f log", true},
		{"wc -l file", "wc -l file", true},
		{"which ls", "which ls", true},
		{"whoami", "whoami", true},
		{"date", "date", true},
		{"env", "env", true},
		{"printenv", "printenv", true},
		{"tree", "tree", true},
		{"stat file", "stat file", true},
		{"file file", "file file", true},
		{"du -sh dir", "du -sh dir", true},
		{"df -h", "df -h", true},
		{"ps aux", "ps aux", true},
		{"uname -a", "uname -a", true},

		// --- 带多余空白 ---
		{"leading space", "  ls", true},
		{"trailing space", "ls  ", true},
		{"extra spaces between", "ls   -la", true},
		{"tab separated", "ls\t-la", true},
		{"newlines", "\n  ls \n", true},

		// --- 链式/重定向 (不应视为只读) ---
		{"pipe", "ls | grep foo", false},
		{"semicolon", "ls; echo done", false},
		{"ampersand background", "ls &", false},
		{"redirect out", "cat file > out.txt", false},
		{"redirect in", "cat < file.txt", false},
		{"herestring", "cat <<< hello", false},
		{"backtick subshell", "echo $(pwd)", false},
		{"double paren", "echo $((1+1))", false},
		{"newline in command", "ls\necho hello", false},
		{"pipe with redirect", "ls | cat > out", false},

		// --- git 命令 ---
		{"git status", "git status", true},
		{"git log", "git log", true},
		{"git diff", "git diff", true},
		{"git show HEAD", "git show HEAD", true},
		{"git branch", "git branch", true},
		{"git remote -v", "git remote -v", true},
		{"git config --list", "git config --list", true},
		{"git rev-parse HEAD", "git rev-parse HEAD", true},
		{"git commit", "git commit", false},
		{"git push", "git push", false},
		{"git checkout main", "git checkout main", false},
		{"git reset --hard", "git reset --hard", false},
		{"git with chain", "git log | head", false},

		// --- version/help 探针 (任何命令) ---
		{"--version", "cat --version", true},
		{"version flag", "python version", true},
		{"--help", "node --help", true},
		{"ls --version", "ls --version", true},

		// --- 解释器 (go/node/python/python3) ---
		{"go version", "go version", true},
		{"go env", "go env", true},
		{"go vet", "go vet", true},
		{"go list", "go list", true},
		{"go run main.go", "go run main.go", false},
		{"go build", "go build", false},
		{"go mod tidy", "go mod tidy", false},
		{"node --version", "node --version", true},
		{"node -V", "node -V", true},
		{"node index.js", "node index.js", false},
		{"python --version", "python --version", true},
		{"python -V", "python -V", true},
		{"python script.py", "python script.py", false},
		{"python3 --version", "python3 --version", true},
		{"python3 -V", "python3 -V", true},
		{"python3 script.py", "python3 script.py", false},

		// --- 未知命令 / 其他写操作 ---
		{"mkdir", "mkdir newdir", false},
		{"rm -rf", "rm -rf tmp", false},
		{"curl", "curl -X POST http://x", false},
		{"wget", "wget http://x", false},
		{"touch", "touch f", false},
		{"cp", "cp a b", false},
		{"mv", "mv a b", false},
		{"tee", "tee file", false},

		// --- 边界情况 ---
		{"empty string", "", false},
		{"just spaces", "   ", false},
		{"just newlines", "\n\n", false},
		{"single word no match", "foobar", false},
		{"single dash", "-", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsReadOnly(tc.command)
			if got != tc.want {
				t.Errorf("IsReadOnly(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}
