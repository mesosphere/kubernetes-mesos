package scheduler

import (
	"sync/atomic"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
)

type OfferFilter func(*mesos.Offer) bool

type OfferRegistry interface {
	// Initialize the instance, spawning necessary housekeeping go routines.
	Init()
	Add([]*mesos.Offer)
	AwaitNew(id string, f OfferFilter) <-chan empty

	// invoked when offers are rescinded or expired
	Delete(string)

	Get(offerId string) (PerishableOffer, bool)

	Walk(Walker) error

	// invalidate one or all (when offerId="") offers; offers are not declined,
	// but are simply flagged as expired in the offer history
	Invalidate(offerId string)
}

// callback that is invoked during a walk through a series of active offers,
// returning with stop=true when the walk should stop.
type Walker func(offer PerishableOffer) (stop bool, err error)

type OfferRegistryConfig struct {
	declineOffer  func(offerId string) error
	ttl           time.Duration // determines a perishable offer's expiration deadline: now+ttl
	lingerTtl     time.Duration // if zero, offers will not linger in the FIFO past their expiration deadline
	walkDelay     time.Duration // specifies the sleep time between expired offer harvests
	listenerDelay time.Duration // specifies the sleep time between offer listener notifications
}

type offerStorage struct {
	OfferRegistryConfig
	offers    *cache.FIFO // collection of timedOffer's
	listeners *cache.FIFO // collection of <-chan empty
}

type timedOffer struct {
	*mesos.Offer
	expiration time.Time
	acquired   int32 // 1 = acquired, 0 = free
}

type expiredOffer struct{}

type PerishableOffer interface {
	// returns true if this offer has expired
	hasExpired() bool
	// if not yet expired, return mesos offer details; otherwise nil
	details() *mesos.Offer
	// mark this offer as acquired, returning true if it was previously unacquired. thread-safe.
	acquire() bool
	// mark this offer as un-acquired. thread-safe.
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

func (e *expiredOffer) release() {}

func (to *timedOffer) hasExpired() bool {
	return time.Now().After(to.expiration)
}

func (to *timedOffer) details() *mesos.Offer {
	return to.Offer
}

func (to *timedOffer) acquire() bool {
	return atomic.CompareAndSwapInt32(&to.acquired, 0, 1) && !to.hasExpired()
}

func (to *timedOffer) release() {
	atomic.CompareAndSwapInt32(&to.acquired, 1, 0)
}

func CreateOfferRegistry(c OfferRegistryConfig) OfferRegistry {
	return &offerStorage{c, cache.NewFIFO(), cache.NewFIFO()}
}

func (s *offerStorage) Add(offers []*mesos.Offer) {
	now := time.Now()
	for _, offer := range offers {
		offerId := offer.Id.GetValue()
		log.V(3).Infof("Receiving offer %v", offerId)
		s.offers.Add(offerId, &timedOffer{offer, now.Add(s.ttl), 0})
	}
}

// delete an offer from storage, meaning that we expire it
func (s *offerStorage) Delete(offerId string) {
	if offer, ok := s.Get(offerId); ok {
		log.V(3).Infof("Deleting offer %v", offerId)
		// attempt to block others from consuming the offer. if it's already been
		// claimed and is not yet lingering then don't decline it - just mark it as
		// expired in the history: allow a prior claimant to attempt to launch with it
		myoffer := offer.acquire()
		if offer.details() != nil && myoffer {
			log.V(3).Infof("Declining offer %v", offerId)
			if err := s.declineOffer(offerId); err != nil {
				log.Warningf("Failed to decline offer %v: %v", offerId, err)
			}
		}
		s.expireOffer(offer)
	} // else, ignore offers not in the history
}

// expire all known, live offers
func (s *offerStorage) Invalidate(offerId string) {
	if offerId != "" {
		s.invalidateOne(offerId)
		return
	}
	obj := s.offers.List()
	for _, o := range obj {
		offer, ok := o.(PerishableOffer)
		if !ok {
			log.Errorf("Expected perishable offer, not %v", o)
			continue
		}
		offer.acquire() // attempt to block others from using it
		s.expireOffer(offer)
		// don't reject it from the controller, we already know that it's an invalid offer
	}
}

func (s *offerStorage) invalidateOne(offerId string) {
	if offer, ok := s.Get(offerId); ok {
		offer.acquire() // attempt to block others from using it
		s.expireOffer(offer)
		// don't reject it from the controller, we already know that it's an invalid offer
	}
}

// Walk the collection of offers, Delete()ing those that have expired,
// visiting those that haven't with the Walked. The walk stops either as
// indicated by the Walker or when the end of the offer list is reached.
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

func (s *offerStorage) expireOffer(offer PerishableOffer) {
	// the offer may or may not be expired due to TTL so check for details
	// since that's a more reliable determinant of lingering status
	if details := offer.details(); details != nil {
		// recently expired, should linger
		offerId := details.Id.GetValue()
		log.V(3).Infof("Expiring offer %v", offerId)
		if s.lingerTtl > 0 {
			log.V(3).Infof("offer will linger: %v", offerId)
			s.offers.Update(offerId, &expiredOffer{})
			go func() {
				time.Sleep(s.lingerTtl)
				log.V(3).Infof("Delete lingering offer: %v", offerId)
				s.offers.Delete(offerId)
			}()
		} else {
			log.V(3).Infof("Permanently deleting offer %v", offerId)
			s.offers.Delete(offerId)
		}
	} // else, it's still lingering...
}

func (s *offerStorage) Get(id string) (PerishableOffer, bool) {
	if obj, ok := s.offers.Get(id); !ok {
		return nil, false
	} else {
		to, ok := obj.(PerishableOffer)
		if !ok {
			log.Errorf("invalid offer object in fifo '%v'", obj)
		}
		return to, ok
	}
}

type offerListener struct {
	id     string
	filter OfferFilter
	notify chan<- empty
	age    int
}

// register a listener for new offers, whom we'll notify upon receiving such.
func (s *offerStorage) AwaitNew(id string, f OfferFilter) <-chan empty {
	if f == nil {
		return nil
	}
	ch := make(chan empty, 1)
	listen := &offerListener{
		id:     id,
		filter: f,
		notify: ch,
		age:    0,
	}
	log.V(3).Infof("Registering offer listener %s", listen.id)
	s.listeners.Add(id, listen)
	return ch
}

func (s *offerStorage) makeOfferHarvestFunc() func() {
	return func() {
		s.Walk(func(PerishableOffer) (bool, error) {
			// noop; simply walking will expire offers past their TTL
			return false, nil
		})
	}
}

func (s *offerStorage) makeOfferListenerFunc() func() {
	return func() {
		var listen *offerListener
		var ok bool
		// get the next offer listener
		for {
			obj := s.listeners.Pop()
			if listen, ok = obj.(*offerListener); ok {
				break
			}
			log.Warningf("unexpected listener object %v", obj)
		}
		// notify if we find an acceptable offer
		for id := range s.offers.Contains() {
			var offer PerishableOffer
			if offer, ok = s.Get(id); !ok || offer.hasExpired() {
				continue
			}
			if listen.filter(offer.details()) {
				log.V(3).Infof("Notifying offer listener %s", listen.id)
				listen.notify <- empty{}
				return
			}
		}
		// no interesting offers found, re-queue the listener
		listen.age++
		if listen.age < 20 {
			// if the same listener has re-registered in the meantime we don't want to
			// destroy the newer listener channel. this is racy since a newer listener
			// can register between the Get() and Update(), but the consequences aren't
			// very dire - the listener merely has to wait their full backoff period.
			if _, ok := s.listeners.Get(listen.id); !ok {
				log.V(3).Infof("Re-registering offer listener %s", listen.id)
				s.listeners.Update(listen.id, listen)
			}
		} // else, you're gc'd
	}
}

func (s *offerStorage) Init() {
	go util.Forever(s.makeOfferHarvestFunc(), s.walkDelay)

	// periodically gc the listeners FIFO.
	// to avoid a rush on offers, we add a short delay between each registered
	// listener, so as to allow the most recently notified listener a bit of time
	// to act on the offer.
	go util.Forever(s.makeOfferListenerFunc(), s.listenerDelay)
}
