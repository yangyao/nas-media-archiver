package main

import "net/http"

func canDeleteServiceJob(j *ServiceJob) bool {
	allowed := j.Status == "completed" || j.Status == "completed_with_errors" || (!j.Started && (j.Status == "ready" || j.Status == "interrupted" || j.Status == "failed"))
	if !allowed {
		return false
	}
	// Incomplete publications need their journal for safe retry/recovery.
	for _, f := range j.Files {
		if f.Status != "completed" && (f.StagingPath != "" || f.Published) {
			return false
		}
	}
	return true
}
func (s *archiveService) deleteJobEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	j := s.jobs[id]
	if j == nil {
		s.mu.Unlock()
		serviceError(w, 404, "not_found", "任务不存在，可能已被删除。")
		return
	}
	if s.closing || !canDeleteServiceJob(j) {
		s.mu.Unlock()
		serviceError(w, 409, "invalid_state", "只能删除未开始或已完成的任务；执行中或仍需恢复的任务不能删除。")
		return
	}
	done := s.store.deleteJob(id)
	delete(s.jobs, id)
	s.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			serviceError(w, 503, "save_failed", "删除记录暂未保存，请检查服务端存储状态。")
			return
		}
		writeServiceJSON(w, 200, map[string]string{"id": id, "status": "deleted"})
	case <-r.Context().Done():
		// The queued deletion is not canceled when a client disconnects.
	}
}
