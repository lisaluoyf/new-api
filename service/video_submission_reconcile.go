package service

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var videoSubmissionReconcileOnce sync.Once

// Reconcile persisted evidence only. Unknown upstream acceptance never causes
// a generation POST, inferred rejection, a second reservation or a debit.
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
