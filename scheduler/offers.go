package scheduler

import (
	"sync/atomic"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
)

type OfferRegistry interface {
	Add([]*mesos.Offer)

	// invoked when offers are rescinded, expired, or otherwise rejected
	Delete(string)

	Get(offerId string) (perishableOffer, bool)

	Walk(Walker) error

	Invalidate()
	InvalidateOne(string)
}

// callback that is invoked during a walk through a series of active offers,
// returning with stop=true when the walk should stop.
type Walker func(offer perishableOffer) (stop bool, err error)

type OfferRegistryConfig struct {
	declineOffer func(offerId string) error
	ttl          time.Duration // determines a perishable offer's expiration deadline: now+ttl
	lingerTtl    time.Duration // if zero, offers will not linger in the FIFO past their expiration deadline
}

type offerStorage struct {
	OfferRegistryConfig
	offers *cache.FIFO // collection of timedOffer's
}

type timedOffer struct {
	*mesos.Offer
	expiration time.Time
	acquired   int32 // 1 = acquired, 0 = free
}

type expiredOffer struct{}

type perishableOffer interface {
	hasExpired() bool
	details() *mesos.Offer
	acquire() bool
	release()
}

func (e *expiredOffer) hasExpired() bool {
	return true
}

func (e *expiredOffer) details() *mesos.Offer {
	return nil
}

func (e *expiredOffer) acquire() bool {
	return false
}

func (e *expiredOffer) release() {
}

func (to *timedOffer) hasExpired() bool {
	return time.Now().After(to.expiration)
}

func (to *timedOffer) details() *mesos.Offer {
	return to.Offer
}

func (to *timedOffer) acquire() bool {
	return atomic.CompareAndSwapInt32(&to.acquired, 0, 1)
}

func (to *timedOffer) release() {
	atomic.CompareAndSwapInt32(&to.acquired, 1, 0)
}

func CreateOfferRegistry(c OfferRegistryConfig) OfferRegistry {
	return &offerStorage{c, cache.NewFIFO()}
}

func (s *offerStorage) Add(offers []*mesos.Offer) {
	now := time.Now()
	for _, offer := range offers {
		offerId := offer.Id.GetValue()
		s.offers.Add(offerId, &timedOffer{offer, now.Add(s.ttl), 0})
	}
}

// delete an offer from storage, meaning that we expire it
func (s *offerStorage) Delete(offerId string) {
	if offer, ok := s.Get(offerId); ok {
		offer.acquire() // attempt to block others from using it
		if !offer.hasExpired() {
			if err := s.declineOffer(offerId); err != nil {
				log.Warningf("Failed to decline offer %v: %v", offerId, err)
			}
		}
		s.handleExpired(offer)
	}
}

// expire all known, live offers
func (s *offerStorage) Invalidate() {
	obj := s.offers.List()
	for _, o := range obj {
		offer, ok := o.(perishableOffer)
		if !ok {
			log.Errorf("Expected perishable offer, not %v", o)
			continue
		}
		offer.acquire() // attempt to block others from using it
		s.handleExpired(offer)
		// don't reject it from the controller, we already know that it's an invalid offer
	}
}

func (s *offerStorage) InvalidateOne(offerId string) {
	if offer, ok := s.Get(offerId); ok {
		offer.acquire() // attempt to block others from using it
		s.handleExpired(offer)
		// don't reject it from the controller, we already know that it's an invalid offer
	}
}

func (s *offerStorage) Walk(w Walker) error {
	for offerId := range s.offers.Contains() {
		offer, ok := s.Get(offerId)
		if !ok {
			// offer disappeared...
			continue
		}
		if offer.hasExpired() {
			s.Delete(offerId)
			continue
		}
		if stop, err := w(offer); err != nil {
			return err
		} else if stop {
			return nil
		}
	}
	return nil
}

func (s *offerStorage) handleExpired(offer perishableOffer) {
	// the offer may or may not be expired due to TTL so check for details
	// since that's a more reliable determinant of lingering status
	if details := offer.details(); details != nil {
		// recently expired, should linger
		offerId := details.Id.GetValue()
		if s.lingerTtl > 0 {
			log.V(2).Infof("offer will linger: %v", offerId)
			s.offers.Update(offerId, &expiredOffer{})
			go func() {
				time.Sleep(s.lingerTtl)
				log.V(2).Infof("offer will linger: %v", offerId)
				s.offers.Delete(offerId)
			}()
		} else {
			s.offers.Delete(offerId)
		}
	} // else, it's still lingering...
}

func (s *offerStorage) Get(id string) (perishableOffer, bool) {
	if obj, ok := s.offers.Get(id); !ok {
		return nil, false
	} else {
		to, ok := obj.(perishableOffer)
		if !ok {
			log.Errorf("invalid offer object in fifo '%v'", obj)
		}
		return to, ok
	}
}

// TODO(jdef): implement an offer watcher that walks the offer list every so often (1s?) to decline expired, unacquired offers
