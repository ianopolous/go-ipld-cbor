package cbornode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	cid "github.com/ipfs/go-cid"
	node "github.com/ipfs/go-ipld-node"
	mh "github.com/multiformats/go-multihash"
	cbor "github.com/whyrusleeping/cbor/go"
)

func Decode(b []byte) (*Node, error) {
	out := new(Node)
	err := cbor.Loads(b, &out.obj)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func EncodeObject(obj interface{}) ([]byte, error) {
	return cbor.Dumps(obj)
}

// DecodeInto decodes a serialized ipld cbor object into the given object.
func DecodeInto(b []byte, v interface{}) error {
	// The cbor library really doesnt make this sort of operation easy on us when we are implementing
	// the `ToCBOR` method.
	var m map[interface{}]interface{}
	err := cbor.Loads(b, &m)
	if err != nil {
		return err
	}

	jsonable, err := toSaneMap(m)
	if err != nil {
		return err
	}

	jsonb, err := json.Marshal(jsonable)
	if err != nil {
		return err
	}

	return json.Unmarshal(jsonb, v)
}

var ErrNoSuchLink = errors.New("no such link found")

type Node struct {
	obj map[interface{}]interface{}
}

func WrapMap(m map[interface{}]interface{}) (*Node, error) {
	return &Node{m}, nil
}

type Link struct {
	Target *cid.Cid `json:"/" cbor:"/"`
}

func (l *Link) ToCBOR(w io.Writer, enc *cbor.Encoder) error {
	obj := map[string]interface{}{
		"/": l.Target.Bytes(),
	}

	return enc.Encode(obj)
}

func (n Node) Resolve(path []string) (interface{}, []string, error) {
	cur := n.obj
	for i, val := range path {
		next, ok := cur[val]
		if !ok {
			return nil, nil, ErrNoSuchLink
		}

		nextmap, ok := next.(map[interface{}]interface{})
		if !ok {
			return nil, nil, errors.New("tried to resolve through object that had no links")
		}

		if lnk, ok := nextmap["/"]; ok {
			out, err := linkCast(lnk)
			if err != nil {
				return nil, nil, err
			}

			out.Name = val
			return out, path[i+1:], nil
		}

		cur = nextmap
	}

	return nil, nil, errors.New("could not resolve through object")
}

func (n Node) ResolveLink(path []string) (*node.Link, []string, error) {
	obj, rest, err := n.Resolve(path)
	if err != nil {
		return nil, nil, err
	}

	lnk, ok := obj.(*node.Link)
	if ok {
		return lnk, rest, nil
	}

	return nil, rest, fmt.Errorf("found non-link at given path")
}

func linkCast(lnk interface{}) (*node.Link, error) {
	lnkb, ok := lnk.([]byte)
	if !ok {
		return nil, errors.New("incorrectly formatted link")
	}

	c, err := cid.Cast(lnkb)
	if err != nil {
		return nil, err
	}

	return &node.Link{Cid: c}, nil
}

func (n Node) Tree() []string {
	var out []string
	err := traverse(n.obj, "", func(name string, val interface{}) error {
		out = append(out, name)
		return nil
	})
	if err != nil {
		panic(err)
	}

	return out
}

func (n Node) Links() []*node.Link {
	var out []*node.Link
	err := traverse(n.obj, "", func(_ string, val interface{}) error {
		if lnk, ok := val.(*node.Link); ok {
			out = append(out, lnk)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return out
}

func traverse(obj map[interface{}]interface{}, cur string, cb func(string, interface{}) error) error {
	if lnk, ok := obj["/"]; ok {
		l, err := linkCast(lnk)
		if err != nil {
			return err
		}

		return cb(cur, l)
	}

	for k, v := range obj {
		ks, ok := k.(string)
		if !ok {
			return errors.New("map key was not a string")
		}
		this := cur + "/" + ks
		switch v := v.(type) {
		case map[interface{}]interface{}:
			if err := traverse(v, this, cb); err != nil {
				return err
			}
		default:
			if err := cb(this, v); err != nil {
				return err
			}
		}
	}

	return nil
}

func (n Node) RawData() []byte {
	b, err := cbor.Dumps(n.obj)
	if err != nil {
		// not sure this can ever happen
		panic(err)
	}

	return b
}

func (n Node) Cid() *cid.Cid {
	data := n.RawData()
	hash, _ := mh.Sum(data, mh.SHA2_256, -1)
	return cid.NewCidV1(cid.CBOR, hash)
}

func (n Node) Loggable() map[string]interface{} {
	return map[string]interface{}{
		"node_type": "cbor",
		"cid":       n.Cid(),
	}
}

func (n Node) Size() (uint64, error) {
	return uint64(len(n.RawData())), nil
}

func (n Node) Stat() (*node.NodeStat, error) {
	return &node.NodeStat{}, nil
}

func (n Node) String() string {
	return n.Cid().String()
}

func (n Node) MarshalJSON() ([]byte, error) {
	out, err := toSaneMap(n.obj)
	if err != nil {
		return nil, err
	}

	return json.Marshal(out)
}

func toSaneMap(n map[interface{}]interface{}) (interface{}, error) {
	if lnk, ok := n["/"]; ok && len(n) == 1 {
		lnkb, ok := lnk.([]byte)
		if !ok {
			return nil, fmt.Errorf("link value should have been bytes")
		}

		c, err := cid.Cast(lnkb)
		if err != nil {
			return nil, err
		}

		return &Link{c}, nil
	}
	out := make(map[string]interface{})
	for k, v := range n {
		ks, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("map keys must be strings")
		}

		obj, err := convertToJsonIsh(v)
		if err != nil {
			return nil, err
		}

		out[ks] = obj
	}

	return out, nil
}

func convertToJsonIsh(v interface{}) (interface{}, error) {
	switch v := v.(type) {
	case map[interface{}]interface{}:
		return toSaneMap(v)
	case []interface{}:
		var out []interface{}
		for _, i := range v {
			obj, err := convertToJsonIsh(i)
			if err != nil {
				return nil, err
			}

			out = append(out, obj)
		}
		return out, nil
	default:
		return v, nil
	}
}

var _ node.Node = (*Node)(nil)