package offers

import (
	"errors"
	"testing"
	"time"

	mesos "github.com/mesos/mesos-go/mesosproto"
	util "github.com/mesos/mesos-go/mesosutil"
	"github.com/mesosphere/kubernetes-mesos/pkg/proc"
)

func TestTimedOffer(t *testing.T) {
	t.Parallel()

	ttl := 2 * time.Second
	now := time.Now()
	o := &liveOffer{nil, now.Add(ttl), 0}

	if o.HasExpired() {
		t.Errorf("offer ttl was %v and should not have expired yet", ttl)
	}
	if !o.Acquire() {
		t.Fatal("1st acquisition of offer failed")
	}
	o.Release()
	if !o.Acquire() {
		t.Fatal("2nd acquisition of offer failed")
	}
	if o.Acquire() {
		t.Fatal("3rd acquisition of offer passed but prior claim was not released")
	}
	o.Release()
	if !o.Acquire() {
		t.Fatal("4th acquisition of offer failed")
	}
	o.Release()
	time.Sleep(ttl)
	if !o.HasExpired() {
		t.Fatal("offer not expired after ttl passed")
	}
	if !o.Acquire() {
		t.Fatal("5th acquisition of offer failed; should not be tied to expiration")
	}
	if o.Acquire() {
		t.Fatal("6th acquisition of offer succeeded; should already be acquired")
	}
} // TestTimedOffer

func TestWalk(t *testing.T) {
	t.Parallel()
	config := RegistryConfig{
		DeclineOffer: func(offerId string) <-chan error {
			return proc.ErrorChan(nil)
		},
		TTL:           0 * time.Second,
		LingerTTL:     0 * time.Second,
		ListenerDelay: 0 * time.Second,
	}
	storage := CreateRegistry(config)
	acceptedOfferId := ""
	walked := 0
	walker1 := func(p Perishable) (bool, error) {
		walked++
		if p.Acquire() {
			acceptedOfferId = p.Details().Id.GetValue()
			return true, nil
		}
		return false, nil
	}
	// sanity check
	err := storage.Walk(walker1)
	if err != nil {
		t.Fatalf("received impossible error %v", err)
	}
	if walked != 0 {
		t.Fatal("walked empty storage")
	}
	if acceptedOfferId != "" {
		t.Fatal("somehow found an offer when registry was empty")
	}
	impl, ok := storage.(*offerStorage)
	if !ok {
		t.Fatal("unexpected offer storage impl")
	}
	// single offer
	ttl := 2 * time.Second
	now := time.Now()
	o := &liveOffer{&mesos.Offer{Id: util.NewOfferID("foo")}, now.Add(ttl), 0}

	impl.offers.Add(o)
	err = storage.Walk(walker1)
	if err != nil {
		t.Fatalf("received impossible error %v", err)
	}
	if walked != 1 {
		t.Fatalf("walk count %d", walked)
	}
	if acceptedOfferId != "foo" {
		t.Fatalf("found offer %v", acceptedOfferId)
	}

	acceptedOfferId = ""
	err = storage.Walk(walker1)
	if err != nil {
		t.Fatalf("received impossible error %v", err)
	}
	if walked != 2 {
		t.Fatalf("walk count %d", walked)
	}
	if acceptedOfferId != "" {
		t.Fatalf("found offer %v", acceptedOfferId)
	}

	walker2 := func(p Perishable) (bool, error) {
		walked++
		return true, nil
	}
	err = storage.Walk(walker2)
	if err != nil {
		t.Fatalf("received impossible error %v", err)
	}
	if walked != 3 {
		t.Fatalf("walk count %d", walked)
	}
	if acceptedOfferId != "" {
		t.Fatalf("found offer %v", acceptedOfferId)
	}

	walker3 := func(p Perishable) (bool, error) {
		walked++
		return true, errors.New("baz")
	}
	err = storage.Walk(walker3)
	if err == nil {
		t.Fatal("expected error")
	}
	if walked != 4 {
		t.Fatalf("walk count %d", walked)
	}
}
