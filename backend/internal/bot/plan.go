package bot

import (
	"encoding/json"
	"errors"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"time"
)

var errPlan = errors.New("invalid delivery plan")

func activeDelivery(d Delivery) (Delivery, error) {
	if d.Method != "plan" {
		return d, nil
	}
	if len(d.Steps) == 0 || len(d.Steps) > 100 || d.Cursor < 0 || d.Cursor >= len(d.Steps) {
		return d, errPlan
	}
	step := d.Steps[d.Cursor]
	if step.Method == "plan" {
		return d, errPlan
	}
	step.Guard = d.Guard
	if fmtNumber(step.Payload["chat_id"]) != fmtNumber(d.Payload["chat_id"]) {
		return d, errPlan
	}
	return step, nil
}
func advancePlan(d Delivery, fallback bool) (workqueue.Outcome, error) {
	if d.Method != "plan" {
		return workqueue.Outcome{}, nil
	}
	delay := time.Duration(0)
	if fallback {
		step := d.Steps[d.Cursor]
		if len(step.Fallback) == 0 || len(step.Fallback) > 100 {
			return workqueue.Outcome{Permanent: true}, errPlan
		}
		next := append([]Delivery{}, d.Steps[:d.Cursor]...)
		for i := range step.Fallback {
			stripUnsupported(step.Fallback[i].Payload)
		}
		next = append(next, step.Fallback...)
		d.Steps = append(next, d.Steps[d.Cursor+1:]...)
		if len(d.Steps) > 100 {
			return workqueue.Outcome{Permanent: true}, errPlan
		}
	} else {
		delay = time.Duration(d.Steps[d.Cursor].Delay) * time.Second
		d.Cursor++
	}
	if d.Cursor >= len(d.Steps) {
		return workqueue.Outcome{}, nil
	}
	raw, e := json.Marshal(d)
	if e != nil || len(raw) > 1<<20 {
		return workqueue.Outcome{Permanent: true}, errPlan
	}
	return workqueue.Outcome{NextPayload: raw, NextDelay: delay}, nil
}

func stripUnsupported(v any) {
	switch n := v.(type) {
	case map[string]any:
		delete(n, "style")
		delete(n, "icon_custom_emoji_id")
		delete(n, "disabled")
		for _, child := range n {
			stripUnsupported(child)
		}
	case []any:
		for _, child := range n {
			stripUnsupported(child)
		}
	}
}
