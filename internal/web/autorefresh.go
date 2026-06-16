package web

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const autoRefreshTimeout = 30 * time.Second

func (a *App) Start(ctx context.Context) {
	go a.autoRefreshLoop(ctx)
}

func (a *App) autoRefreshLoop(ctx context.Context) {
	for {
		config := a.currentWebConfig()
		if !config.AutoRefresh.Enabled {
			select {
			case <-ctx.Done():
				return
			case <-a.autoRefreshWakeCh:
				continue
			}
		}

		next, err := nextDailyRefresh(time.Now(), config.AutoRefresh.Time)
		if err != nil {
			a.logger.Printf("自动刷新配置无效：%v", err)
			select {
			case <-ctx.Done():
				return
			case <-a.autoRefreshWakeCh:
				continue
			}
		}

		wait := time.Until(next)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-a.autoRefreshWakeCh:
			timer.Stop()
			continue
		case <-timer.C:
			a.runAutoRefresh(ctx)
		}
	}
}

func (a *App) runAutoRefresh(ctx context.Context) {
	refreshCtx, cancel := context.WithTimeout(ctx, autoRefreshTimeout)
	defer cancel()

	a.logger.Println("开始执行每日自动刷新 VPNGate 节点列表……")
	if err := a.Refresh(refreshCtx); err != nil {
		a.logger.Printf("每日自动刷新 VPNGate 节点列表失败：%v", err)
		return
	}
	a.logger.Println("每日自动刷新 VPNGate 节点列表完成")
}

func (a *App) currentWebConfig() webConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.config
}

func (a *App) notifyAutoRefreshChanged() {
	select {
	case a.autoRefreshWakeCh <- struct{}{}:
	default:
	}
}

func nextDailyRefresh(now time.Time, value string) (time.Time, error) {
	hour, minute, err := parseAutoRefreshClock(value)
	if err != nil {
		return time.Time{}, err
	}

	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}

	return next, nil
}

func parseAutoRefreshClock(value string) (int, int, error) {
	value = strings.TrimSpace(value)
	if err := validateAutoRefreshTime(value); err != nil {
		return 0, 0, err
	}

	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("自动刷新时间必须使用 HH:MM 格式")
	}

	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}

	return hour, minute, nil
}
