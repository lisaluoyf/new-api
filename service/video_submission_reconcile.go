package service

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var videoSubmissionReconcileOnce sync.Once

// Restore original tasks from persisted acceptance/settlement evidence only.
// Unknown acceptance never causes a generation POST, a new reservation or debit.
func StartVideoSubmissionReconcileTask() {
	if !common.IsMasterNode {
		return
	}
	videoSubmissionReconcileOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if err := model.ReconcileVideoSubmissions(200); err != nil {
					common.SysLog("video submission reconciliation failed: " + err.Error())
				}
				<-ticker.C
			}
		}()
	})
}
