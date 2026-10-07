package domain_test

import (
	"testing"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

func TestInvalidCalendarDate(t *testing.T) {
	const biz = "日历日确定性"
	for _, s := range []string{"2026-02-30", "2026-13-01", "not-a-date", "2026-1-1"} {
		if _, err := domain.ParseDate(s); err == nil {
			t.Fatalf("非法日期 %q 必须报错", s)
		}
		t.Logf("[业务:%s][用例:非法日期解析] PASS - %q 被拒绝", biz, s)
	}
	d, err := domain.ParseDate("2026-10-08")
	if err != nil {
		t.Fatal(err)
	}
	if d.AddDays(10).DaysSince(d) != 10 {
		t.Fatal("日期加减不确定")
	}
	b, _ := d.MarshalText()
	if string(b) != "2026-10-08" {
		t.Fatalf("日期序列化错误: %s", b)
	}
	var d2 domain.Date
	if err := d2.UnmarshalText([]byte("")); err != nil || !d2.IsZero() {
		t.Fatal("空日期应反序列化为零值而不是报错")
	}
	t.Logf("[业务:%s][用例:日期运算与序列化] PASS - 加减天/YYYY-MM-DD/空值均确定", biz)
}
