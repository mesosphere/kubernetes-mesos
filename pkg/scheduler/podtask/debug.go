package podtask

import (
	"fmt"
	"io"
	"net/http"

	log "github.com/golang/glog"
)

//TODO(jdef) we use a Locker to guard against concurrent task state changes, but it would be
//really, really nice to avoid doing this. Maybe someday the registry won't return data ptrs
//but plain structs instead.
func InstallDebugHandlers(reg Registry, mux *http.ServeMux) {
	mux.HandleFunc("/debug/registry/tasks", func(w http.ResponseWriter, r *http.Request) {
		//TODO(jdef) support filtering tasks based on status
		alltasks := reg.List(nil)
		io.WriteString(w, fmt.Sprintf("task_count=%d\n", len(alltasks)))
		for _, task := range alltasks {
			if err := func() (err error) {
				podName := task.Pod.Name
				podNamespace := task.Pod.Namespace
				offerId := ""
				if task.Offer != nil {
					offerId = task.Offer.Id()
				}
				_, err = io.WriteString(w, fmt.Sprintf("%v\t%v/%v\t%v\t%v\n", task.ID, podNamespace, podName, task.State, offerId))
				return
			}(); err != nil {
				log.Warningf("aborting debug handler: %v", err)
				break // stop listing on I/O errors
			}
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
}
