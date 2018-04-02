package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/boltdb/bolt"
)

// 区块链数据库的文件名称
const dbFile = "blockchain.db"

// 区块在 boltDB 中 Bucket 的名称
const blocksBucket = "blocks"

// 创世区块所包含的消息
const genesisCoinbaseData = "The Times 03/Jan/2009 Chancellor on brink of second bailout for banks"

// Blockchain keeps a sequence of Blocks
// 区块链结构体
type Blockchain struct {
	tip []byte   // 最新的区块的哈希值
	db  *bolt.DB // 存放区块的数据库文件
}

// BlockchainIterator is used to iterate over blockchain blocks
// 区块链迭代器，用于依次访问从最新到最旧的全部区块
type BlockchainIterator struct {
	currentHash []byte
	db          *bolt.DB
}

// MineBlock mines a new block with the provided transactions
// 以 transactions 为内容，挖掘新的区块
func (bc *Blockchain) MineBlock(transactions []*Transaction) {
	// 最新的区块的哈希值
	// 下一个区块会用到
	var lastHash []byte

	// 从保存区块链的数据库获取最新的区块的哈希值
	err := bc.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(blocksBucket))
		lastHash = b.Get([]byte("l"))

		return nil
	})

	if err != nil {
		log.Panic(err)
	}

	// 生成新的区块
	newBlock := NewBlock(transactions, lastHash)

	err = bc.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(blocksBucket))
		// 把最新生成的区块保存到数据库中
		// 以最新区块的哈希值为键
		// 以最新区块的序列化的值为值
		err := b.Put(newBlock.Hash, newBlock.Serialize())
		if err != nil {
			log.Panic(err)
		}

		// 把最新区块的哈希值，作为数据库中 "l" 键的值
		err = b.Put([]byte("l"), newBlock.Hash)
		if err != nil {
			log.Panic(err)
		}

		// 以最新区块的哈希值，更新 bc.tip
		bc.tip = newBlock.Hash

		return nil
	})

	if err != nil {
		log.Panic(err)
	}

}

// FindUnspentTransactions returns a list of transactions containing unspent outputs
// 返回结果中的所有 unspent outputs 都要能够被 address 解锁
func (bc *Blockchain) FindUnspentTransactions(address string) []Transaction {
	// 存放含有未被引用的输出的所有交易
	var unspentTXs []Transaction
	// 存放所有已被引用的输出
	spentTXOs := make(map[string][]int)
	// 区块链的迭代器
	bci := bc.Iterator()

	for {
		// 从最新的区块开始迭代
		block := bci.Next()

		// 遍历此 block 的所有交易
		for _, tx := range block.Transactions {
			// 首先获取每个交易的 ID
			// 像 hex.EncodeToString(tx.ID) 这样处理一下
			// 可以让 tx.ID 好看一点
			// 另外 tx.ID 是 []byte 类型，不能作为 map 的 key
			txID := hex.EncodeToString(tx.ID)

		Outputs:
			// 遍历此交易的所有输出
			for outIdx, out := range tx.Vout {
				// 如果 spentTXOs 中存在 txID 的记录
				if spentTXOs[txID] != nil {
					// 遍历此 txID 的所有记录
					for _, spentOut := range spentTXOs[txID] {
						// 如果存在一样的索引号
						// 则跳过这个交易
						if spentOut == outIdx {
							continue Outputs
						}
					}
				}

				// 如果输出 out 可以被 address 解锁
				// 说明此 out 是address 还没有花的钱
				if out.CanBeUnlockedWith(address) {
					// 把这个交易放入 unspentTXs
					unspentTXs = append(unspentTXs, *tx)
				}
			}
			// 如果 tx 不是 Coinbase 交易的话
			// tx 一定是对别的交易的输出进行了引用
			// 要把这些交易找出来，放入 spentTXOs
			if tx.IsCoinbase() == false {
				// 对于此交易中的所有的 input
				for _, in := range tx.Vin {
					// 如果 in 能由 address 生成
					if in.CanUnlockOutputWith(address) {
						// 获取 input 所引用的 output 所在交易的 ID
						inTxID := hex.EncodeToString(in.Txid)
						spentTXOs[inTxID] = append(spentTXOs[inTxID], in.Vout)
					}
				}
			}
		}

		if len(block.PrevBlockHash) == 0 {
			// 已经遍历完成了所有区块
			// 结束循环
			break
		}
	}

	// 返回所有没有花费的交易
	return unspentTXs
}

// FindUTXO finds and returns all unspent transaction outputs
func (bc *Blockchain) FindUTXO(address string) []TXOutput {
	// 收集所有未花费的输出
	var UTXOs []TXOutput
	// 收集所有含有 address 的未花费输出的交易
	unspentTransactions := bc.FindUnspentTransactions(address)

	// 遍历所有含有 address 的未花费输出的交易
	for _, tx := range unspentTransactions {
		// 遍历交易中的所有输出
		for _, out := range tx.Vout {
			// 如果输出能够被 address 解锁 → 这是 address 的未花费的输出
			if out.CanBeUnlockedWith(address) {
				//  把输出 out 放入 UTXOs 中
				UTXOs = append(UTXOs, out)
			}
		}
	}

	// 返回所有找到的 address 的未花费的输出
	return UTXOs
}

// FindSpendableOutputs 寻找并返回没有花掉的输出
// TODO: 弄清楚这个方法
func (bc *Blockchain) FindSpendableOutputs(address string, amount int) (int, map[string][]int) {
	// 没有被引用的输出们
	unspentOutputs := make(map[string][]int)
	// 包含 address 可以引用的输出的交易们
	unspentTXs := bc.FindUnspentTransactions(address)
	// 所有未引用输出的累计数量
	accumulated := 0

Work:
	for _, tx := range unspentTXs {
		// 获取此交易的 ID
		txID := hex.EncodeToString(tx.ID)
		// 对于此交易的每一个输出而言
		for outIdx, out := range tx.Vout {
			// 如果输出能被 address 解锁 → 说明，这是 address 的钱
			// 且，还没有累计到所需的数量
			if out.CanBeUnlockedWith(address) && accumulated < amount {
				// 那就算上这个输出吧
				accumulated += out.Value
				// 并把这个交易带上
				unspentOutputs[txID] = append(unspentOutputs[txID], outIdx)
				// TODO: 核查此处代码
				// unspentOutputs[txID] = append(unspentOutputs[txID], out.Value)

				if accumulated >= amount {
					break Work
				}
			}
		}
	}

	// 返回已经累计的钱数 和 包含这些钱的输出们
	return accumulated, unspentOutputs
}

// Iterator 返回区块链的迭代器
func (bc *Blockchain) Iterator() *BlockchainIterator {
	bci := &BlockchainIterator{bc.tip, bc.db}

	return bci
}

// Next returns next block starting from the tip
func (i *BlockchainIterator) Next() *Block {
	var block *Block

	err := i.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(blocksBucket))
		encodedBlock := b.Get(i.currentHash)
		block = DeserializeBlock(encodedBlock)

		return nil
	})

	if err != nil {
		log.Panic(err)
	}

	i.currentHash = block.PrevBlockHash

	return block
}

func dbExists() bool {
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		return false
	}
	return true
}

// NewBlockchain 使用 genesis Block 创建一条新的区块
func NewBlockchain(address string) *Blockchain {
	// 如果保存区块链的数据库不存在
	// 通知用户并就终止程序
	if dbExists() == false {
		fmt.Println("没有找到区块链数据库。请先创建一个")
		os.Exit(1)
	}

	// tip 是最新的区块的哈希值
	var tip []byte
	// 读取数据库文件
	db, err := bolt.Open(dbFile, 0600, nil)
	if err != nil {
		log.Panic(err)
	}

	// 获取数据库中的最新的区块的哈希值
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(blocksBucket))
		tip = b.Get([]byte("l"))
		return nil
	})
	if err != nil {
		log.Panic(err)
	}

	// 利用 tip 和 db 生成数据库对象
	bc := Blockchain{
		tip: tip,
		db:  db,
	}

	return &bc
}

// CreateBlockchain 创建一个新的区块链数据库文件
func CreateBlockchain(address string) *Blockchain {
	// 如果数据库文件存在，说明区块链已经存在
	// 没必要重新创建区块链数据库
	if dbExists() {
		fmt.Println("区块链已经存在")
		os.Exit(1)
	}

	// 最新的区块的哈希值
	var tip []byte
	// 创建数据库文件
	db, err := bolt.Open(dbFile, 0600, nil)
	if err != nil {
		log.Panic(err)
	}

	// 往数据库中，添加创世区块的内容
	err = db.Update(func(tx *bolt.Tx) error {
		// 生成创世区块的 coinbase 交易
		cbtx := NewCoinbaseTX(address, genesisCoinbaseData)
		// 生成创世区块
		genesis := NewGenesisBlock(cbtx)

		// 创建数据库文件中的 bucket
		b, err := tx.CreateBucket([]byte(blocksBucket))
		if err != nil {
			log.Panic(err)
		}

		// 把创世区块存入数据库
		err = b.Put(genesis.Hash, genesis.Serialize())
		if err != nil {
			log.Panic(err)
		}

		// 把创世区块的哈希值，作为 "l" 的值
		// 存入数据库
		err = b.Put([]byte("l"), genesis.Hash)
		if err != nil {
			log.Panic(err)
		}

		// 把创世区块的哈希值作为 tip 的值
		tip = genesis.Hash

		return nil
	})

	if err != nil {
		log.Panic(err)
	}

	// 利用 tip 和 db 创建区块链
	bc := Blockchain{
		tip: tip,
		db:  db,
	}

	return &bc
}