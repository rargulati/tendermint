package kv

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/abci/types"
	"github.com/tendermint/tendermint/state/txindex"
	"github.com/tendermint/tendermint/types"
	cmn "github.com/tendermint/tmlibs/common"
	db "github.com/tendermint/tmlibs/db"
	"github.com/tendermint/tmlibs/pubsub/query"
)

func TestTxIndex(t *testing.T) {
	indexer := NewTxIndex(db.NewMemDB())

	tx := types.Tx("HELLO WORLD")
	txResult := &types.TxResult{Height: 1, Index: 0, Tx: tx, Result: abci.ResponseDeliverTx{Data: []byte{0}, Code: abci.CodeTypeOK, Log: "", Tags: []cmn.KVPair{}, Fee: cmn.KI64Pair{Key: []uint8{}, Value: 0}}}
	hash := tx.Hash()

	batch := txindex.NewBatch(1)
	if err := batch.Add(txResult); err != nil {
		t.Error(err)
	}
	err := indexer.AddBatch(batch)
	require.NoError(t, err)

	loadedTxResult, err := indexer.Get(hash)
	require.NoError(t, err)
	assert.Equal(t, txResult, loadedTxResult)

	tx2 := types.Tx("BYE BYE WORLD")
	txResult2 := &types.TxResult{Height: 1, Index: 0, Tx: tx2, Result: abci.ResponseDeliverTx{Data: []byte{0}, Code: abci.CodeTypeOK, Log: "", Tags: []cmn.KVPair{}, Fee: cmn.KI64Pair{Key: []uint8{}, Value: 0}}}
	hash2 := tx2.Hash()

	err = indexer.Index(txResult2)
	require.NoError(t, err)

	loadedTxResult2, err := indexer.Get(hash2)
	require.NoError(t, err)
	assert.Equal(t, txResult2, loadedTxResult2)
}

func TestTxSearch(t *testing.T) {
	allowedTags := []string{"account.number", "account.owner", "account.date"}
	indexer := NewTxIndex(db.NewMemDB(), IndexTags(allowedTags))

	txResult := txResultWithTags([]cmn.KVPair{
		{Key: []byte("account.number"), Value: []byte("1")},
		{Key: []byte("account.owner"), Value: []byte("Ivan")},
		{Key: []byte("not_allowed"), Value: []byte("Vlad")},
	})
	hash := txResult.Tx.Hash()

	err := indexer.Index(txResult)
	require.NoError(t, err)

	testCases := []struct {
		q             string
		resultsLength int
	}{
		// search by hash
		{fmt.Sprintf("tx.hash = '%X'", hash), 1},
		// search by exact match (one tag)
		{"account.number = 1", 1},
		// search by exact match (two tags)
		{"account.number = 1 AND account.owner = 'Ivan'", 1},
		// search by exact match (two tags)
		{"account.number = 1 AND account.owner = 'Vlad'", 0},
		// search by range
		{"account.number >= 1 AND account.number <= 5", 1},
		// search by range (lower bound)
		{"account.number >= 1", 1},
		// search by range (upper bound)
		{"account.number <= 5", 1},
		// search using not allowed tag
		{"not_allowed = 'boom'", 0},
		// search for not existing tx result
		{"account.number >= 2 AND account.number <= 5", 0},
		// search using not existing tag
		{"account.date >= TIME 2013-05-03T14:45:00Z", 0},
		// search using CONTAINS
		{"account.owner CONTAINS 'an'", 1},
		// search using CONTAINS
		{"account.owner CONTAINS 'Vlad'", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.q, func(t *testing.T) {
			results, err := indexer.Search(query.MustParse(tc.q))
			assert.NoError(t, err)

			assert.Len(t, results, tc.resultsLength)
			if tc.resultsLength > 0 {
				assert.Equal(t, []*types.TxResult{txResult}, results)
			}
		})
	}
}

func TestTxSearchOneTxWithMultipleSameTagsButDifferentValues(t *testing.T) {
	allowedTags := []string{"account.number"}
	indexer := NewTxIndex(db.NewMemDB(), IndexTags(allowedTags))

	txResult := txResultWithTags([]cmn.KVPair{
		{Key: []byte("account.number"), Value: []byte("1")},
		{Key: []byte("account.number"), Value: []byte("2")},
	})

	err := indexer.Index(txResult)
	require.NoError(t, err)

	results, err := indexer.Search(query.MustParse("account.number >= 1"))
	assert.NoError(t, err)

	assert.Len(t, results, 1)
	assert.Equal(t, []*types.TxResult{txResult}, results)
}

func TestTxSearchMultipleTxs(t *testing.T) {
	allowedTags := []string{"account.number"}
	indexer := NewTxIndex(db.NewMemDB(), IndexTags(allowedTags))

	// indexed first, but bigger height (to test the order of transactions)
	txResult := txResultWithTags([]cmn.KVPair{
		{Key: []byte("account.number"), Value: []byte("1")},
	})
	txResult.Tx = types.Tx("Bob's account")
	txResult.Height = 2
	err := indexer.Index(txResult)
	require.NoError(t, err)

	// indexed second, but smaller height (to test the order of transactions)
	txResult2 := txResultWithTags([]cmn.KVPair{
		{Key: []byte("account.number"), Value: []byte("2")},
	})
	txResult2.Tx = types.Tx("Alice's account")
	txResult2.Height = 1
	err = indexer.Index(txResult2)
	require.NoError(t, err)

	results, err := indexer.Search(query.MustParse("account.number >= 1"))
	assert.NoError(t, err)

	require.Len(t, results, 2)
	assert.Equal(t, []*types.TxResult{txResult2, txResult}, results)
}

func TestIndexAllTags(t *testing.T) {
	indexer := NewTxIndex(db.NewMemDB(), IndexAllTags())

	txResult := txResultWithTags([]cmn.KVPair{
		cmn.KVPair{[]byte("account.owner"), []byte("Ivan")},
		cmn.KVPair{[]byte("account.number"), []byte("1")},
	})

	err := indexer.Index(txResult)
	require.NoError(t, err)

	results, err := indexer.Search(query.MustParse("account.number >= 1"))
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, []*types.TxResult{txResult}, results)

	results, err = indexer.Search(query.MustParse("account.owner = 'Ivan'"))
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, []*types.TxResult{txResult}, results)
}

func txResultWithTags(tags []cmn.KVPair) *types.TxResult {
	tx := types.Tx("HELLO WORLD")
	return &types.TxResult{Height: 1, Index: 0, Tx: tx, Result: abci.ResponseDeliverTx{Data: []byte{0}, Code: abci.CodeTypeOK, Log: "", Tags: tags, Fee: cmn.KI64Pair{Key: []uint8{}, Value: 0}}}
}

func benchmarkTxIndex(txsCount int, b *testing.B) {
	tx := types.Tx("HELLO WORLD")
	txResult := &types.TxResult{Height: 1, Index: 0, Tx: tx, Result: abci.ResponseDeliverTx{Data: []byte{0}, Code: abci.CodeTypeOK, Log: "", Tags: []cmn.KVPair{}, Fee: cmn.KI64Pair{Key: []uint8{}, Value: 0}}}

	dir, err := ioutil.TempDir("", "tx_index_db")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir) // nolint: errcheck

	store := db.NewDB("tx_index", "leveldb", dir)
	indexer := NewTxIndex(store)

	batch := txindex.NewBatch(txsCount)
	for i := 0; i < txsCount; i++ {
		if err := batch.Add(txResult); err != nil {
			b.Fatal(err)
		}
		txResult.Index++
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		err = indexer.AddBatch(batch)
	}
	if err != nil {
		b.Fatal(err)
	}
}

func BenchmarkTxIndex1(b *testing.B)     { benchmarkTxIndex(1, b) }
func BenchmarkTxIndex500(b *testing.B)   { benchmarkTxIndex(500, b) }
func BenchmarkTxIndex1000(b *testing.B)  { benchmarkTxIndex(1000, b) }
func BenchmarkTxIndex2000(b *testing.B)  { benchmarkTxIndex(2000, b) }
func BenchmarkTxIndex10000(b *testing.B) { benchmarkTxIndex(10000, b) }
