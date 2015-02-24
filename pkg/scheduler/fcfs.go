package scheduler

import (
	"fmt"
	log "github.com/golang/glog"

	"github.com/mesosphere/kubernetes-mesos/pkg/offers"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/podtask"
)

// A first-come-first-serve scheduler: acquires the first offer that can support the task
func FCFSScheduleFunc(r offers.Registry, unused SlaveIndex, task *podtask.T) (offers.Perishable, error) {
	if task.HasAcceptedOffer() {
		// verify that the offer is still on the table
		offerId := task.GetOfferId()
		if offer, ok := r.Get(offerId); ok && !offer.HasExpired() {
			// skip tasks that have already have assigned offers
			return task.Offer, nil
		}
		task.Offer.Release()
		task.ClearTaskInfo()
	}

	var acceptedOffer offers.Perishable
	err := r.Walk(func(p offers.Perishable) (bool, error) {
		offer := p.Details()
		if offer == nil {
			return false, fmt.Errorf("nil offer while scheduling task %v", task.ID)
		}
		if task.AcceptOffer(offer) {
			if p.Acquire() {
				acceptedOffer = p
				log.V(3).Infof("Pod %v accepted offer %v", task.Pod.Name, offer.Id.GetValue())
				return true, nil // stop, we found an offer
			}
		}
		return false, nil // continue
	})
	if acceptedOffer != nil {
		if err != nil {
			log.Warningf("problems walking the offer registry: %v, attempting to continue", err)
		}
		return acceptedOffer, nil
	}
	if err != nil {
		log.V(2).Infof("failed to find a fit for pod: %v, err = %v", task.Pod.Name, err)
		return nil, err
	}
	log.V(2).Infof("failed to find a fit for pod: %v", task.Pod.Name)
	return nil, noSuitableOffersErr
}
