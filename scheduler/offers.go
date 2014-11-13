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
	Init()
	Add([]*mesos.Offer)
	AwaitNew(id string, f OfferFilter) <-chan empty

	// invoked when offers are rescinded, expired, or otherwise rejected
	Delete(string)

	Get(offerId string) (PerishableOffer, bool)

	Walk(Walker) error

	Invalidate()
	InvalidateOne(string)
}

// callback that is invoked during a walk through a series of active offers,
// returning with stop=true when the walk should stop.
type Walker func(offer PerishableOffer) (stop bool, err error)

type OfferRegistryConfig struct {
	declineOffer func(offerId string) error
	ttl          time.Duration // determines a perishable offer's expiration deadline: now+ttl
	lingerTtl    time.Duration // if zero, offers will not linger in the FIFO past their expiration deadline
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
	return &offerStorage{c, cache.NewFIFO(), cache.NewFIFO()}
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
		s.expireOffer(offer)
	}
}

// expire all known, live offers
func (s *offerStorage) Invalidate() {
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

func (s *offerStorage) InvalidateOne(offerId string) {
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

func (s *offerStorage) Init() {
	// periodically gc the listeners FIFO.
	// to avoid a rush on offers, we add a short delay between each registered
	// listener, so as to allow the most recently notified listener a bit of time
	// to act on the offer.
	go util.Forever(func() {
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
	}, 1*time.Second)
}
