package codeintel

import "testing"

// TruncateWithTotal 的第二个返回值是这个包里 8 处缺陷的共同修法，所以判据守在这里，
// 不守在 8 个工具里——同 INV-EXEC-PROC-01：只有一个地方能做，不是记得加那个字段。
//
// 缺陷的形状（2026-09-20 普查）：前一版签名把总数丢在函数内部，于是每个调用点都得
// 记得先存一份 len()。8/18 没记得，而漏掉不报错、不打日志，只是那个数字变小了：
//   - find_references  "Found 3 reference(s)" 实际 30 → 模型据此改签名 → 编译不过
//   - find_callers     "Found 5 caller(s)" 实际 20 → 删掉还在被调用的函数
//   - find_orphans     "Found 5 orphan(s)" 实际 40 → 清理完 5 个以为干净了
//   - read_symbols     问 8 个符号答 5 个，且一句话都没说

func TestTruncateWithTotal(t *testing.T) {
	t.Run("超限时截断并报出原始总数", func(t *testing.T) {
		in := make([]int, 20)
		shown, total := TruncateWithTotal(in, VerbositySummary)
		if len(shown) != 5 {
			t.Errorf("summary 档应截到 5，得到 %d", len(shown))
		}
		if total != 20 {
			t.Errorf("total = %d，要 20——这个数字是整个修法的理由", total)
		}
	})

	t.Run("未超限时 total 等于长度，不是 0", func(t *testing.T) {
		// total=0 会让 CountPhrase 与 ListItems 都把完整列表当成"不知道总数"，
		// 于是要么少报截断、要么多报。零值在这里不是中性状态。
		in := make([]int, 3)
		shown, total := TruncateWithTotal(in, VerbositySummary)
		if len(shown) != 3 || total != 3 {
			t.Errorf("要 3/3，得到 %d/%d", len(shown), total)
		}
	})

	t.Run("空切片", func(t *testing.T) {
		shown, total := TruncateWithTotal([]int{}, VerbosityDetail)
		if len(shown) != 0 || total != 0 {
			t.Errorf("要 0/0，得到 %d/%d", len(shown), total)
		}
	})

	t.Run("nil 切片不 panic", func(t *testing.T) {
		shown, total := TruncateWithTotal[int](nil, VerbosityFull)
		if shown != nil || total != 0 {
			t.Errorf("nil 应原样返回，得到 %v/%d", shown, total)
		}
	})

	t.Run("三档上限", func(t *testing.T) {
		in := make([]int, 100)
		for _, c := range []struct {
			v    VerbosityLevel
			want int
		}{
			{VerbositySummary, 5},
			{VerbosityDetail, 15},
			{VerbosityFull, 50},
		} {
			shown, total := TruncateWithTotal(in, c.v)
			if len(shown) != c.want {
				t.Errorf("%s 档上限 = %d，要 %d", c.v, len(shown), c.want)
			}
			if total != 100 {
				t.Errorf("%s 档 total = %d，要 100", c.v, total)
			}
		}
	})

	t.Run("不改原切片的长度视图", func(t *testing.T) {
		// items[:limit] 共享底层数组是有意的（零拷贝），但调用方拿到的 total
		// 必须仍然是原长度，否则"截断了多少"就无从计算。
		in := make([]int, 20)
		_, total := TruncateWithTotal(in, VerbositySummary)
		if len(in) != 20 || total != 20 {
			t.Errorf("原切片被改动了：len(in)=%d total=%d", len(in), total)
		}
	})
}

func TestCountPhrase(t *testing.T) {
	for _, c := range []struct {
		shown, total int
		want         string
	}{
		{5, 20, "5 of 20"},
		{3, 3, "3"},
		{0, 0, "0"},
		{0, 7, "0 of 7"},
		// total 小于 shown 是调用方算错了。此时不产出 "5 of 1" 这种矛盾数字——
		// 它会让用户以为 UI 坏了，而那比不报截断更糟。
		{5, 1, "5"},
		// total 为 0 而 shown 非 0：同上，按"不知道总数"处理
		{5, 0, "5"},
	} {
		if got := CountPhrase(c.shown, c.total); got != c.want {
			t.Errorf("CountPhrase(%d, %d) = %q，要 %q", c.shown, c.total, got, c.want)
		}
	}
}
