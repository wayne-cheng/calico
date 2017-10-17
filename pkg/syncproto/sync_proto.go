// Copyright (c) 2017 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The syncproto package defines the structs used in the Felix/Typha protocol.
//
// Overview
//
// Felix connects to Typha over a TCP socket, then Felix initiates the (synchronous)
// handshake consisting of a ClientHello then a ServerHello message.
//
// Once the handshake is complete, Typha sends a series of KV pairs to Felix,
// amounting to a complete snapshot of the datastore.  It may send more than one
// KV message, each containing one or more KV pairs.
//
// Once a complete snapshot has been sent, Typha sends a SyncStatus message with
// its current sync status.  This is typically "InSync" but it may be another status,
// such as "Resync" if Typha itself is resyncing with the datastore.
//
// At any point after the  handshake, Typha may send a Ping message, which Felix
// should respond to as quickly as possible with a Pong (if Typha doesn't receive
// a timely response it may terminate the connection).
//
// After the initial snapshot is sent, Typha sends KVs and SyncStatus messages
// as new updates are received from the datastore.
//
//	+-------+                +-------+
//	| Felix |                | Typha |
//	+-------+                +-------+
//	|                        |
//	| connect                |
//	|----------------------->|
//	|                        | -------------------------------\
//	|                        |-| accept, wait for ClientHello |
//	|                        | |------------------------------|
//	|                        |
//	| ClientHello            |
//	|----------------------->|
//	|                        |
//	|            ServerHello |
//	|<-----------------------|
//	|                        | ------------------------------------\
//	|                        |-| start KV send & pinger goroutines |
//	|                        | |-----------------------------------|
//	|                        |
//	|                KVs * n |
//	|<-----------------------|
//	|                        |
//	|                   Ping |
//	|<-----------------------|
//	|                        |
//	| Pong                   |
//	|----------------------->|
//	|                        |
//	|                KVs * n |
//	|<-----------------------|
//	|                        |
//	|     SyncStatus(InSync) |
//	|<-----------------------|
//	|                        |
//	|                KVs * n |
//	|<-----------------------|
//	|                        |
//
// Wire format
//
// The protocol uses gob to encode messages.  Each message is wrapped in an Envelope
// struct to simplify decoding.
//
// Key/value pairs are encoded as SerializedUpdate objects.  These contain the KV pair
// along with the Syncer metadata about the update (such as its revision and update type).
// The key and value are encoded to the libcalico-go "default" encoding, as used when
// storing data in, for example, etcd.  I.e. the gob struct contains string and []byte
// fields to hold the key and value, respectively.  Doing this has some advantages:
//
//     (1) It avoids any subtle incompatibility between our datamodel and gob.
//
//     (2) It removes the need to register all our datatypes with the gob en/decoder.
//
//     (3) It re-uses known-good serialization code with known semantics around
//         data-model upgrade.  I.e. since that serialization is based on the JSON
//         marshaller, we know how it treats added/removed fields.
//
//     (4) It allows us to do the serialization of each KV pair once and send it to
//         all listening clients.  (Doing this in gob is not easy because the gob
//         connection is stateful.)
//
// Upgrading the datamodel
//
// Some care needs to be taken when upgrading Felix and Typha to ensure that datamodel
// changes are correctly handled.
//
// Since Typha parses resources from the datamodel and then serializes them again,
//
//     - Typha can only pass through resources (and fields) that were present in the
//       version of libcalico-go that it was compiled against.
//
//     - Similarly, Felix can only parse resources and fields that were present in the
//       version of libcalico-go that it was compiled against.
//
//     - It is important that even synthesized resources (for example, those that are
//       generated by the Kubernetes datastore driver) are serializable, even if we never
//       normally write them to a key/value based datastore such as etcd.
//
// In the common case, where a new field is added to the datamodel:
//
//     - If a new Felix connects to an old Typha then Typha will strip the new field
//       at parse-time and pass the object through to Felix.  Hence Felix will behave
//       as if the field wasn't present.  As long as the field was added in a back-compatible
//       way, Felix should default to its old behaviour and the overall outcome will be
//       that new Felix will behave as if it was an old Felix.
//
//     - If an old Felix connects to a new Typha, then Typha will pass through the new
//       field to Felix but Felix will strip it out when it parses the update.
//
// Where a whole new resource is added:
//
//     - If a new Felix connects to an old Typha then Typha will ignore the new resource
//       so it is important that Felix is engineered to allow for missing resources in
//       that case.
//
//     - If an old Felix connects to a new Typha then Typha will send the resource
//       but the old Felix will fail to parse it.  In that case, the Typha client code
//       used by Felix drops the KV pair and logs an error.
//
// In more complicated cases: it's important to think through how the above cases play out.
// For example, removing one synthesized resource type and adding another to take its
// place may no longer work as intended since the new one will get stripped out when
// a mixed Typha/Felix version connection occurs.
//
// If such a change does need to be made, we could treat it as a Typha protocol upgrade
// as described below.
//
// Upgrading the Typha protocol
//
// Currently, the Typha protocol is unversioned.  It is important that an uplevel Typha
// doesn't send a new uplevel message to a downlevel Felix or vice-versa since the gob
// decoder would fail to parse the message, resulting in closing the connection.
//
// If we need to add new unsolicited messages in either direction, we could add a
// ProtocolVersion field to the handshake messages.  Since gob defaults fields to
// their zero value if they're not present on the wire, a Typha with a ProtocolVersion
// field that receives a connection from an old Felix with no field would see 0 as the
// value of the field and could act accordingly.
//
// If a more serious upgrade is needed (such as replacing gob), we could use a second
// port for the new protocol.
package syncproto

import (
	"encoding/gob"
	"errors"
	"fmt"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
)

// Source code for the Sequence diagram above (http://textart.io/sequence).
const _ = `
object Felix Typha
Felix->Typha: connect
note right of Typha: accept, wait for ClientHello
Felix->Typha: ClientHello
Typha->Felix: ServerHello
note right of Typha: start KV send & pinger goroutines
Typha->Felix: KVs * n
Typha->Felix: Ping
Felix->Typha: Pong
Typha->Felix: KVs * n
Typha->Felix: SyncStatus(InSync)
Typha->Felix: KVs * n
`

const DefaultPort = 5473

type Envelope struct {
	Message interface{}
}

type MsgClientHello struct {
	Hostname string
	Info     string
	Version  string
}
type MsgServerHello struct {
	Version string
}
type MsgSyncStatus struct {
	SyncStatus api.SyncStatus
}
type MsgPing struct {
	Timestamp time.Time
}
type MsgPong struct {
	PingTimestamp time.Time
	PongTimestamp time.Time
}
type MsgKVs struct {
	KVs []SerializedUpdate
}

func init() {
	// We need to use RegisterName here to force the name to be equal, even if this package gets vendored since the
	// default name would include the vendor directory.
	gob.RegisterName("github.com/projectcalico/typha/pkg/syncproto.MsgClientHello", MsgClientHello{})
	gob.RegisterName("github.com/projectcalico/typha/pkg/syncproto.MsgServerHello", MsgServerHello{})
	gob.RegisterName("github.com/projectcalico/typha/pkg/syncproto.MsgSyncStatus", MsgSyncStatus{})
	gob.RegisterName("github.com/projectcalico/typha/pkg/syncproto.MsgPing", MsgPing{})
	gob.RegisterName("github.com/projectcalico/typha/pkg/syncproto.MsgPong", MsgPong{})
	gob.RegisterName("github.com/projectcalico/typha/pkg/syncproto.MsgKVs", MsgKVs{})
}

func SerializeUpdate(u api.Update) (su SerializedUpdate, err error) {
	su.Key, err = model.KeyToDefaultPath(u.Key)
	if err != nil {
		log.WithError(err).WithField("update", u).Error(
			"Bug: failed to serialize key that was generated by Syncer.")
		return
	}

	su.TTL = u.TTL
	su.Revision = u.Revision // This relies on the revision being a basic type.
	su.UpdateType = u.UpdateType

	if u.Value == nil {
		log.Debug("Value is nil, passing through as a deletion.")
		return
	}

	value, err := model.SerializeValue(&u.KVPair)
	if err != nil {
		log.WithError(err).WithField("update", u).Error(
			"Bug: failed to serialize value, using nil value (to simulate deletion).")
		err = nil
		return
	}
	su.Value = value

	return
}

type SerializedUpdate struct {
	Key        string
	Value      []byte
	Revision   string
	TTL        time.Duration
	UpdateType api.UpdateType
}

var ErrBadKey = errors.New("Unable to parse key.")

func (s SerializedUpdate) ToUpdate() (api.Update, error) {
	// Parse the key.
	parsedKey := model.KeyFromDefaultPath(s.Key)
	if parsedKey == nil {
		log.WithField("key", s.Key).Error("BUG: cannot parse key.")
		return api.Update{}, ErrBadKey
	}
	var parsedValue interface{}
	if s.Value != nil {
		var err error
		parsedValue, err = model.ParseValue(parsedKey, s.Value)
		if err != nil {
			log.WithField("rawValue", string(s.Value)).Error(
				"Failed to parse value.")
		}
	}
	return api.Update{
		KVPair: model.KVPair{
			Key:      parsedKey,
			Value:    parsedValue,
			Revision: s.Revision,
			TTL:      s.TTL,
		},
		UpdateType: s.UpdateType,
	}, nil
}

// WouldBeNoOp returns true if this update would be a no-op given that previous has already been sent.
func (s SerializedUpdate) WouldBeNoOp(previous SerializedUpdate) bool {
	// We don't care if the revision has changed so nil it out.  Note: we're using the fact that this is a
	// value type so these changes won't be propagated to the caller!
	s.Revision = ""
	previous.Revision = ""

	if previous.UpdateType == api.UpdateTypeKVNew {
		// If the old update was a create, convert it to an update before the comparison since it's OK to
		// squash an update to a new key if the value hasn't changed.
		previous.UpdateType = api.UpdateTypeKVUpdated
	}

	// TODO Typha Add UT to make sure that the serialization is always the same (current JSON impl does make that guarantee)
	return reflect.DeepEqual(s, previous)
}

func (s SerializedUpdate) String() string {
	return fmt.Sprintf("SerializedUpdate<Key:%s, Value:%s, Revision:%v, TTL:%v, UpdateType:%v>",
		s.Key, string(s.Value), s.Revision, s.TTL, s.UpdateType)
}
