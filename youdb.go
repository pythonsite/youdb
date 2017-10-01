// Package youdb is a Bolt wrapper that allows easy store hash, zset data.
package youdb

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/boltdb/bolt"
	"reflect"
	"strconv"
	"time"
	"unsafe"
)

const (
	replyOK                 = "ok"
	replyNotFound           = "not_found"
	replyError              = "error"
	replyClientError        = "client_error"
	bucketNotFound          = "bucket_not_found"
	keyNotFound             = "key_not_found"
	scoreMin         uint64 = 0
	scoreMax         uint64 = 18446744073709551615
)

var (
	hashPrefix     = []byte{30}
	zetKeyPrefix   = []byte{31}
	zetScorePrefix = []byte{29}
)

type (
	bs []byte
	// DB embeds a bolt.DB
	DB struct {
		*bolt.DB
	}

	// Reply a holder for a Entry list of a hashmap
	Reply struct {
		State string
		Data  []bs
	}

	// Entry a key-value pair
	Entry struct {
		Key, Value bs
	}
)

// Open creates/opens a bolt.DB at specified path, and returns a DB enclosing the same
func Open(path string) (*DB, error) {
	database, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}

	db := DB{database}

	return &db, nil
}

// Close closes the embedded bolt.DB
func (db *DB) Close() error {
	return db.DB.Close()
}

// Hset set the byte value in argument as value of the key of a hashmap
func (db *DB) Hset(name string, key, val []byte) error {
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	return db.DB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return err
		}
		return b.Put(key, val)
	})
}

// Hmset set multiple key-value pairs of a hashmap in one method call
func (db *DB) Hmset(name string, kvs ...[]byte) error {
	if len(kvs) == 0 || len(kvs)%2 != 0 {
		return errors.New("kvs len must is an even number")
	}
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	return db.DB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return err
		}
		for i := 0; i < (len(kvs) - 1); i += 2 {
			err := b.Put(kvs[i], kvs[i+1])
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// Hincr increment the number stored at key in a hashmap by step
func (db *DB) Hincr(name string, key []byte, step uint64) (uint64, error) {
	var i uint64
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	err := db.DB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return err
		}
		var oldNum uint64
		v := b.Get(key)
		if v != nil {
			oldNum = B2i(v)
		}
		if step > 0 {
			if (scoreMax - step) < oldNum {
				return errors.New("overflow number")
			}
		} else {
			if (oldNum + step) < scoreMin {
				return errors.New("overflow number")
			}
		}

		oldNum += step
		err = b.Put(key, I2b(oldNum))
		if err != nil {
			return err
		}
		i = oldNum
		return nil
	})
	return i, err
}

// Hdel delete specified key of a hashmap
func (db *DB) Hdel(name string, key []byte) error {
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	return db.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b != nil {
			return b.Delete(key)
		}
		return nil
	})
}

// HdelBucket delete all keys in a hashmap
func (db *DB) HdelBucket(name string) error {
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	return db.DB.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket(bucketName)
	})
}

// Hget get the value related to the specified key of a hashmap
func (db *DB) Hget(name string, key []byte) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		v := b.Get(key)
		if v == nil {
			return errors.New(keyNotFound)
		}
		r.State = replyOK
		r.Data = append(r.Data, v)
		return nil
	})
	if err != nil {
		r.State = err.Error()
	}
	return r
}

// Hmget get the values related to the specified multiple keys of a hashmap
func (db *DB) Hmget(name string, keys [][]byte) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		for _, key := range keys {
			v := b.Get(key)
			if v != nil {
				r.State = replyOK
				r.Data = append(r.Data, key, v)
			}
		}
		return nil
	})
	if err != nil {
		r.State = err.Error()
	}
	return r
}

// Hscan list key-value pairs of a hashmap with keys in range (key_start, key_end]
func (db *DB) Hscan(name string, keyStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		c := b.Cursor()
		n := 0
		for k, v := c.Seek(keyStart); k != nil; k, v = c.Next() {
			if bytes.Compare(k, keyStart) == 1 {
				n++
				r.State = replyOK
				r.Data = append(r.Data, k, v)
				if n == limit {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		r.State = err.Error()
	}
	return r
}

// Hrscan list key-value pairs of a hashmap with keys in range (key_start, key_end], in reverse order
func (db *DB) Hrscan(name string, keyStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	bucketName := Bconcat([][]byte{hashPrefix, S2b(name)})
	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		c := b.Cursor()
		n := 0
		startKey, k0, v0 := []byte{255}, []byte{}, []byte{}
		if len(keyStart) > 0 {
			startKey = make([]byte, len(keyStart))
			copy(startKey, keyStart)
			k0, v0 = c.Seek(startKey)
		} else {
			k0, v0 = c.Last()
		}
		for k, v := k0, v0; k != nil; k, v = c.Prev() {
			if bytes.Compare(k, startKey) == -1 {
				n++
				r.State = replyOK
				r.Data = append(r.Data, k, v)
				if n >= limit {
					break
				}
			}
		}
		return nil
	})
	if err == nil {
		r.State = err.Error()
	}
	return r
}

// Zset set the score of the key of a zset
func (db *DB) Zset(name string, key []byte, val uint64) error {
	score := I2b(val)
	keyBucket := Bconcat([][]byte{zetKeyPrefix, S2b(name)})
	scoreBucket := Bconcat([][]byte{zetScorePrefix, S2b(name)})
	newKey := Bconcat([][]byte{score, key})
	return db.DB.Update(func(tx *bolt.Tx) error {
		b1, err1 := tx.CreateBucketIfNotExists(keyBucket)
		if err1 != nil {
			return err1
		}
		b2, err2 := tx.CreateBucketIfNotExists(scoreBucket)
		if err2 != nil {
			return err2
		}

		oldScore := b2.Get(key)
		if !bytes.Equal(oldScore, score) {
			err1 = b1.Put(newKey, []byte{})
			if err1 != nil {
				return err1
			}

			err2 = b2.Put(key, score)
			if err2 != nil {
				return err2
			}

			if oldScore != nil {
				oldKey := Bconcat([][]byte{oldScore, key})
				err1 = b1.Delete(oldKey)
				if err1 != nil {
					return err1
				}
			}
		}
		return nil
	})
}

// Zmset et multiple key-score pairs of a zset in one method call
func (db *DB) Zmset(name string, kvs ...[]byte) error {
	if len(kvs) == 0 || len(kvs)%2 != 0 {
		return errors.New("kvs len must is an even number")
	}

	keyBucket := Bconcat([][]byte{zetKeyPrefix, S2b(name)})
	scoreBucket := Bconcat([][]byte{zetScorePrefix, S2b(name)})

	return db.DB.Update(func(tx *bolt.Tx) error {
		b1, err1 := tx.CreateBucketIfNotExists(keyBucket)
		if err1 != nil {
			return err1
		}
		b2, err2 := tx.CreateBucketIfNotExists(scoreBucket)
		if err2 != nil {
			return err2
		}

		for i := 0; i < (len(kvs) - 1); i += 2 {
			key := kvs[i]
			score := kvs[i+1]
			newKey := Bconcat([][]byte{score, key})

			oldScore := b2.Get(key)
			if !bytes.Equal(oldScore, score) {
				err1 = b1.Put(newKey, []byte(""))
				if err1 != nil {
					return err1
				}

				err2 = b2.Put(key, score)
				if err2 != nil {
					return err2
				}

				if oldScore != nil {
					oldKey := Bconcat([][]byte{oldScore, key})
					err1 = b1.Delete(oldKey)
					if err1 != nil {
						return err1
					}
				}
			}
		}
		return nil
	})
}

// Zincr increment the number stored at key in a zset by step
func (db *DB) Zincr(name string, key []byte, step uint64) (uint64, error) {
	var score uint64

	keyBucket := Bconcat([][]byte{zetKeyPrefix, S2b(name)})
	scoreBucket := Bconcat([][]byte{zetScorePrefix, S2b(name)})

	err := db.DB.Update(func(tx *bolt.Tx) error {
		b1, err1 := tx.CreateBucketIfNotExists(keyBucket)
		if err1 != nil {
			return err1
		}
		b2, err2 := tx.CreateBucketIfNotExists(scoreBucket)
		if err2 != nil {
			return err2
		}

		vOld := b2.Get(key)
		if vOld != nil {
			score = B2i(vOld)
		}
		if step > 0 {
			if (scoreMax - step) < score {
				return errors.New("overflow number")
			}
		} else {
			if (score + step) < scoreMin {
				return errors.New("overflow number")
			}
		}

		score += step
		newScoreB := I2b(score)
		newKey := Bconcat([][]byte{newScoreB, key})

		err1 = b1.Put(newKey, []byte{})
		if err1 != nil {
			return err1
		}

		err2 = b2.Put(key, newScoreB)
		if err2 != nil {
			return err2
		}

		if vOld != nil {
			oldKey := Bconcat([][]byte{vOld, key})
			err1 = b1.Delete(oldKey)
			if err1 != nil {
				return err1
			}
		}
		return nil
	})
	return score, err
}

// Zdel delete specified key of a zset
func (db *DB) Zdel(name string, key []byte) error {
	keyBucket := Bconcat([][]byte{zetKeyPrefix, S2b(name)})
	scoreBucket := Bconcat([][]byte{zetScorePrefix, S2b(name)})
	return db.DB.Update(func(tx *bolt.Tx) error {
		b1 := tx.Bucket(keyBucket)
		if b1 == nil {
			return nil
		}
		b2 := tx.Bucket(scoreBucket)
		if b2 == nil {
			return nil
		}

		oldScore := b2.Get(key)
		if oldScore != nil {
			oldKey := Bconcat([][]byte{oldScore, key})
			err := b1.Delete(oldKey)
			if err != nil {
				return err
			}
			return b2.Delete(key)
		}
		return nil
	})
}

// ZdelBucket delete all keys in a zset
func (db *DB) ZdelBucket(name string) error {
	keyBucket := Bconcat([][]byte{zetKeyPrefix, S2b(name)})
	scoreBucket := Bconcat([][]byte{zetScorePrefix, S2b(name)})
	return db.DB.Update(func(tx *bolt.Tx) error {
		err := tx.DeleteBucket(keyBucket)
		if err != nil {
			return err
		}
		return tx.DeleteBucket(scoreBucket)
	})
}

// Zget get the score related to the specified key of a zset
func (db *DB) Zget(name string, key []byte) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	scoreBucket := Bconcat([][]byte{zetScorePrefix, S2b(name)})
	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(scoreBucket)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		v := b.Get(key)
		if v != nil {
			r.State = replyOK
			r.Data = append(r.Data, v)
		} else {
			return errors.New(keyNotFound)
		}
		return nil
	})
	if err != nil {
		r.State = err.Error()
	}
	return r
}

// Zmget get the values related to the specified multiple keys of a zset
func (db *DB) Zmget(name string, keys [][]byte) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	scoreBucket := Bconcat([][]byte{zetScorePrefix, S2b(name)})

	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(scoreBucket)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		for _, key := range keys {
			v := b.Get(key)
			if v != nil {
				r.State = replyOK
				r.Data = append(r.Data, key, v)
			}
		}

		return nil
	})
	if err != nil {
		r.State = err.Error()
	}
	return r
}

// Zscan list key-score pairs in a zset, where key-score in range (key_start+score_start, score_end]
func (db *DB) Zscan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	keyBucket := Bconcat([][]byte{zetKeyPrefix, S2b(name)})

	scoreStartB := I2b(scoreMin)
	if len(scoreStart) > 0 {
		scoreStartB = make([]byte, len(scoreStart))
		copy(scoreStartB, scoreStart)
	}

	startScoreKeyB := Bconcat([][]byte{scoreStartB, keyStart})

	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(keyBucket)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		c := b.Cursor()
		n := 0
		// c.Seek(scoreStartB) or c.Seek(startScoreKeyB)
		for k, _ := c.Seek(scoreStartB); k != nil; k, _ = c.Next() {
			if bytes.Compare(k, startScoreKeyB) == 1 {
				n++
				r.State = replyOK
				r.Data = append(r.Data, k[8:], k[0:8])
				if n == limit {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		r.State = err.Error()
	}
	return r
}

// Zrscan list key-score pairs of a zset, in reverse order
func (db *DB) Zrscan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []bs{},
	}
	keyBucket := Bconcat([][]byte{zetKeyPrefix, S2b(name)})

	startKey := []byte{255}
	if len(keyStart) > 0 {
		startKey = make([]byte, len(keyStart))
		copy(startKey, keyStart)
	}

	scoreStartB := I2b(scoreMax)
	if len(scoreStart) > 0 {
		scoreStartB = make([]byte, len(scoreStart))
		copy(scoreStartB, scoreStart)
	}

	startScoreKeyB := Bconcat([][]byte{scoreStartB, startKey})

	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(keyBucket)
		if b == nil {
			return errors.New(bucketNotFound)
		}
		c := b.Cursor()

		k0, v0 := []byte{}, []byte{}
		if len(scoreStart) > 0 {
			k0, v0 = c.Seek(scoreStartB) // or c.Seek(startScoreKeyB)
		} else {
			k0, v0 = c.Last()
		}

		n := 0
		for k, _ := k0, v0; k != nil; k, _ = c.Prev() {
			if bytes.Compare(k, startScoreKeyB) == -1 {
				n++
				r.State = replyOK
				r.Data = append(r.Data, k[8:], k[0:8])
				if n == limit {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		r.State = err.Error()
	}
	return r
}

// String is a convenience wrapper over Get for string value
func (r *Reply) String() string {
	if len(r.Data) > 0 {
		return B2s(r.Data[0])
	}
	return ""
}

// Int is a convenience wrapper over Get for int value of a hashmap
func (r *Reply) Int() int {
	return int(r.Uint64())
}

// Int64 is a convenience wrapper over Get for int64 value of a hashmap
func (r *Reply) Int64() int64 {
	if len(r.Data) < 1 {
		return 0
	}
	return int64(r.Uint64())
}

// Uint is a convenience wrapper over Get for uint value of a hashmap
func (r *Reply) Uint() uint {
	return uint(r.Uint64())
}

// Uint64 is a convenience wrapper over Get for uint64 value of a hashmap
func (r *Reply) Uint64() uint64 {
	if len(r.Data) < 1 {
		return 0
	}
	if len(r.Data[0]) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(r.Data[0])
}

// List retrieves the key/value pairs from reply of a hashmap
func (r *Reply) List() []Entry {
	list := []Entry{}
	if len(r.Data) < 1 {
		return list
	}
	for i := 0; i < (len(r.Data) - 1); i += 2 {
		list = append(list, Entry{r.Data[i], r.Data[i+1]})
	}
	return list
}

// Dict retrieves the key/value pairs from reply of a hashmap
func (r *Reply) Dict() map[string][]byte {
	dict := make(map[string][]byte)
	if len(r.Data) < 1 {
		return dict
	}
	for i := 0; i < (len(r.Data) - 1); i += 2 {
		dict[B2s(r.Data[i])] = r.Data[i+1]
	}
	return dict
}

// JSON parses the JSON-encoded Reply Entry value and stores the result
// in the value pointed to by v
func (r *Reply) JSON(v interface{}) error {
	return json.Unmarshal(r.Data[0], &v)
}

func (r bs) String() string {
	return B2s(r)
}

// Int is a convenience wrapper over Get for int value of a hashmap
func (r bs) Int() int {
	return int(r.Uint64())
}

// Int64 is a convenience wrapper over Get for int64 value of a hashmap
func (r bs) Int64() int64 {
	return int64(r.Uint64())
}

// Uint is a convenience wrapper over Get for uint value of a hashmap
func (r bs) Uint() uint {
	return uint(r.Uint64())
}

// Uint64 is a convenience wrapper over Get for uint64 value of a hashmap
func (r bs) Uint64() uint64 {
	if len(r) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(r)
}

// JSON parses the JSON-encoded Reply Entry value and stores the result
// in the value pointed to by v
func (r bs) JSON(v interface{}) error {
	return json.Unmarshal(r, &v)
}

// Bconcat concat a list of byte
func Bconcat(slices [][]byte) []byte {
	var totalLen int
	for _, s := range slices {
		totalLen += len(s)
	}
	tmp := make([]byte, totalLen)
	var i int
	for _, s := range slices {
		i += copy(tmp[i:], s)
	}
	return tmp
}

// DS2b returns an 8-byte big endian representation of Digit string
// v ("123456") -> uint64(123456) -> 8-byte big endian
func DS2b(v string) ([]byte, error) {
	i, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return nil, err
	}
	return I2b(i), nil
}

// DS2i returns uint64 of Digit string
// v ("123456") -> uint64(123456)
func DS2i(v string) uint64 {
	i, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return uint64(0)
	}
	return i
}

// Itob returns an 8-byte big endian representation of v
// v uint64(123456) -> 8-byte big endian
func I2b(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// Btoi return an int64 of v
// v (8-byte big endian) -> uint64(123456)
func B2i(v []byte) uint64 {
	return binary.BigEndian.Uint64(v)
}

// B2ds return a Digit string of v
// uint64(123456) -> v (8-byte big endian) -> "123456"
func B2ds(v []byte) string {
	return strconv.FormatUint(binary.BigEndian.Uint64(v), 10)
}

// B2s converts byte slice to a string without memory allocation.
// []byte("abc") -> "abc" s
func B2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// S2b converts string to a byte slice without memory allocation.
// "abc" -> []byte("abc")
func S2b(s string) []byte {
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh := reflect.SliceHeader{
		Data: sh.Data,
		Len:  sh.Len,
		Cap:  sh.Len,
	}
	return *(*[]byte)(unsafe.Pointer(&bh))
}
