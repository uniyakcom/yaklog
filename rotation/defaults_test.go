package rotation

import "testing"

// ── applyDefaults 钳位行为 ───────────────────────────────────────────────────

// TestApplyDefaults_MaxBackupsClamped 验证 maxBackups 超限值在 applyDefaults 中被钳位。
func TestApplyDefaults_MaxBackupsClamped(t *testing.T) {
	o := options{maxBackups: maxAllowedBackups + 999}

	applyDefaults(&o)

	if o.maxBackups != maxAllowedBackups {
		t.Errorf("maxBackups 期望被钳位为 %d，得 %d", maxAllowedBackups, o.maxBackups)
	}
}

// TestApplyDefaults_MaxAgeClamped 验证 maxAge 超限值在 applyDefaults 中被钳位。
func TestApplyDefaults_MaxAgeClamped(t *testing.T) {
	o := options{maxAge: maxAllowedMaxAge + 100}

	applyDefaults(&o)

	if o.maxAge != maxAllowedMaxAge {
		t.Errorf("maxAge 期望被钳位为 %d，得 %d", maxAllowedMaxAge, o.maxAge)
	}
}

// TestApplyDefaults_NegativeToZero 验证负值 maxBackups / maxAge 被归零。
func TestApplyDefaults_NegativeToZero(t *testing.T) {
	o := options{maxBackups: -5, maxAge: -10}

	applyDefaults(&o)

	if o.maxBackups != 0 {
		t.Errorf("负 maxBackups 期望归零，得 %d", o.maxBackups)
	}
	if o.maxAge != 0 {
		t.Errorf("负 maxAge 期望归零（defaultMaxAge=0），得 %d", o.maxAge)
	}
}

// TestApplyDefaults_ValidUnchanged 验证合法值不被修改。
func TestApplyDefaults_ValidUnchanged(t *testing.T) {
	o := options{
		maxBackups: 50,
		maxAge:     30,
		maxSize:    1 << 20,
		ext:        ".jsonl",
	}

	applyDefaults(&o)

	if o.maxBackups != 50 {
		t.Errorf("合法 maxBackups 不应被修改：期望 50，得 %d", o.maxBackups)
	}
	if o.maxAge != 30 {
		t.Errorf("合法 maxAge 不应被修改：期望 30，得 %d", o.maxAge)
	}
	if o.maxSize != 1<<20 {
		t.Errorf("合法 maxSize 不应被修改")
	}
	if o.ext != ".jsonl" {
		t.Errorf("已设置的 ext 不应被覆盖：期望 .jsonl，得 %s", o.ext)
	}
}

// TestApplyDefaults_ZeroFillsDefaults 验证零值字段被填入默认值。
func TestApplyDefaults_ZeroFillsDefaults(t *testing.T) {
	o := options{}

	applyDefaults(&o)

	if o.ext != defaultExt {
		t.Errorf("零值 ext 期望填入 %q，得 %q", defaultExt, o.ext)
	}
	if o.maxSize != defaultMaxSize {
		t.Errorf("零值 maxSize 期望填入 %d，得 %d", defaultMaxSize, o.maxSize)
	}
}
