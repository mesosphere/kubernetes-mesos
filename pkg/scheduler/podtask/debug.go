package podtask

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	log "github.com/golang/glog"
)

//TODO(jdef) we use a Locker to guard against concurrent task state changes, but it would be
//really, really nice to avoid doing this. Maybe someday the registry won't return data ptrs
//but plain structs instead.
func InstallDebugHandlers(l sync.Locker, reg Registry) {
	http.HandleFunc("/debug/registry/tasks", func(w http.ResponseWriter, r *http.Request) {
		//TODO(jdef) support filtering tasks based on status
		alltasks := reg.List(nil)
		io.WriteString(w, fmt.Sprintf("task_count=%d\n", len(alltasks)))
		for _, taskid := range alltasks {
			if task, state := reg.Get(taskid); state == StateUnknown {
				log.V(2).Infof("unknown task id: %v", taskid)
				continue
			} else if err := func() (err error) {
				l.Lock()
				defer l.Unlock()

				podName := ""
				podNamespace := ""
				if task.Pod != nil {
					podName = task.Pod.Name
					podNamespace = task.Pod.Namespace
				}
				offerId := ""
				if task.Offer != nil {
					offerId = task.Offer.Id()
				}
				_, err = io.WriteString(w, fmt.Sprintf("%v\t%v/%v\t%v\t%v\n", task.ID, podNamespace, podName, state, offerId))
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
