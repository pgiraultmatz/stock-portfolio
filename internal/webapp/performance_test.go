package webapp

import "testing"

func TestParseTRCSVSortsTransactionsByDateTime(t *testing.T) {
	content := `"datetime","date","account_type","category","type","asset_class","name","symbol","shares","price","amount","fee","tax","currency","original_amount","original_currency","fx_rate","description","transaction_id","counterparty_name","counterparty_iban","payment_reference","mcc_code"
"2026-08-10T15:00:00.000Z","2026-08-10","DEFAULT","TRADING","SELL","STOCK","Bloom Energy","US0937121079","-1.0000000000","130.0000000000","130.00","-1.00","","EUR","","","","Sell late","late","","","",""
"2026-08-10T09:00:00.000Z","2026-08-10","DEFAULT","TRADING","BUY","STOCK","Bloom Energy","US0937121079","1.0000000000","140.0000000000","-140.00","-1.00","","EUR","","","","Buy early","early","","","",""
`

	txs, year, err := parseTRCSV(content)
	if err != nil {
		t.Fatalf("parseTRCSV returned error: %v", err)
	}
	if year != "2026" {
		t.Fatalf("year = %q, want 2026", year)
	}
	if len(txs) != 2 {
		t.Fatalf("len(txs) = %d, want 2", len(txs))
	}
	if txs[0].Type != "BUY" || txs[0].DateTime != "2026-08-10T09:00:00.000Z" {
		t.Fatalf("first transaction = %+v, want 09:00 BUY", txs[0])
	}
	if txs[1].Type != "SELL" || txs[1].DateTime != "2026-08-10T15:00:00.000Z" {
		t.Fatalf("second transaction = %+v, want 15:00 SELL", txs[1])
	}
}
