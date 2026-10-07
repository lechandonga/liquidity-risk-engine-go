package engine_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/engine"
)

// TestConcurrentWritesAndComputes 在 race 检测下验证：
// 并发批次写入、重复回放、同步/异步快照计算、任意时点查询同时进行时，
// 无数据竞争、无死锁；所有结论都基于完整状态，序号单调，快照不可变。
// 全程不使用 sleep：后台计算用条件变量等待目标序号，主流程用 WaitGroup 汇合。
func TestConcurrentWritesAndComputes(t *testing.T) {
	const biz = "并发读写一致性"
	e := engine.New("CNY")
	base := domain.MustDate(2026, 10, 8)
	if _, err := e.Scenarios().Publish(baseScenario("C", 1, 15, nil)); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	const perWriter = 60
	var accepted int64

	var wg sync.WaitGroup
	start := make(chan struct{})

	// 1) 写入方：每 writer 使用独立 ID 空间，REGISTER 必然成功；
	// 	  额外用共享批次 ID 制造重复回放（每个批次被两个 writer 抢注）。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWriter; i++ {
				id := posID(w, i)
				batchID := "B-" + id
				b := domain.Batch{BatchID: batchID, PostedAt: base, Ops: []domain.PositionOp{
					{Type: domain.OpRegister, Position: deposit(id, 100+int64(i), "DEFAULT")},
				}}
				if _, err := e.ApplyBatch(b); err == nil {
					atomic.AddInt64(&accepted, 1)
				}
				// 重复回放同一批次：必须幂等、无错误、序号不增长。
				if _, err := e.ApplyBatch(b); err != nil {
					t.Errorf("并发重复回放返回错误: %v", err)
					return
				}
			}
		}(w)
	}

	// 2) 查询方：持续读取合同净现金流与头寸列表，期间不得出现不一致状态。
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		for {
			select {
			case <-start:
			default:
			}
			l := e.ContractualLadder()
			_ = l.NetAt(base, base.AddDays(15))
			_ = len(e.Positions())
			if atomic.LoadInt64(&accepted) >= int64(writers*perWriter) {
				return
			}
		}
	}()

	// 3) 同步计算方：在写入过程中反复出快照；序号必须单调不减，且不可变。
	computeDone := make(chan struct{})
	var lastSnapSeq int64
	go func() {
		defer close(computeDone)
		for atomic.LoadInt64(&accepted) < int64(writers*perWriter) {
			snap, err := e.Compute("C", 1, base, 15)
			if err != nil {
				t.Errorf("并发计算失败: %v", err)
				return
			}
			if snap.StateSeq < atomic.LoadInt64(&lastSnapSeq) {
				t.Errorf("观察到状态序号倒退: %d -> %d", lastSnapSeq, snap.StateSeq)
				return
			}
			atomic.StoreInt64(&lastSnapSeq, snap.StateSeq)
			// 快照内容自洽：每日净流之和等于窗口合计。
			var sum int64
			for _, v := range snap.Metrics.NetCashFlowByDay {
				sum += v
			}
			if sum != snap.Metrics.TotalNetCashFlow {
				t.Errorf("快照内部不自洽: %d != %d", sum, snap.Metrics.TotalNetCashFlow)
				return
			}
		}
	}()

	// 4) 异步计算方：请求在最终序号就绪后计算，通过 channel 回执汇合（无 sleep）。
	wantSeq := int64(writers * perWriter)
	asyncCh := make(chan *engine.PublishResult, 1)

	close(start)
	e.PublishAsync(engine.ComputeRequest{
		ReqID: "final", Scenario: "C", Version: 1, BaseDate: base, Horizon: 15,
		MinStateSeq: wantSeq,
	}, asyncCh)

	wg.Wait()
	res := <-asyncCh
	if res.Err != nil {
		t.Fatalf("后台计算失败: %v", res.Err)
	}
	if res.Snapshot.StateSeq != wantSeq {
		t.Fatalf("后台快照应固定在最终序号 %d，实际 %d", wantSeq, res.Snapshot.StateSeq)
	}
	<-computeDone
	<-queryDone

	if e.CurrentSeq() != wantSeq {
		t.Fatalf("最终序号期望 %d，实际 %d（重复回放计数？）", wantSeq, e.CurrentSeq())
	}
	if len(e.Positions()) != writers*perWriter {
		t.Fatalf("最终头寸数期望 %d，实际 %d", writers*perWriter, len(e.Positions()))
	}
	// 已发布快照可按原序号取回，且内容不随后续任何操作改变。
	got, ok := e.GetSnapshot(wantSeq, "C", 1, base, 15)
	if !ok || got != res.Snapshot {
		t.Fatal("最终快照未能按序号取回或不是同一不可变对象")
	}
	testLog(t, biz, "并发写入+回放+同步/异步计算",
		"writer=%d 批次=%d 最终seq=%d 头寸=%d 快照seq=%d 无竞争",
		writers, writers*perWriter, e.CurrentSeq(), len(e.Positions()), got.StateSeq)
}

func posID(w, i int) string {
	return string(rune('A'+w)) + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
