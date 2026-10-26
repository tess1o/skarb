package mcp

// Exported aliases for the tool input/output contracts. The telegram package
// uses these to decode structured tool results without duplicating the JSON
// shapes. They intentionally alias the unexported implementation types so the
// MCP server's JSON contract stays the single source of truth.

type (
	AddTransactionItem    = addTransactionItem
	AddTransactionsOutput = addTransactionsOut

	ListTransactionsInput  = listTransactionsIn
	ListTransactionsOutput = listTransactionsOut
	TransactionOutput      = transactionOut

	SummaryOutput       = summaryOut
	CurrencyTotalOutput = currencyTotalOut

	StatsCategoryOutput = statsCategoryOut
	CategoryTotalOutput = categoryTotalOut
	StatsUsersOutput    = statsUsersOut
	UserTotalOutput     = userTotalOut
	StatsCompareOutput  = statsCompareOut
	CategoryDeltaOutput = categoryDeltaOut
	StatsTrendOutput    = statsTrendOut
	TrendPointOutput    = trendPointOut

	NetWorthOutput       = netWorthOut
	AccountBalanceOutput = accountBalanceOut
	SavingsHistoryOutput = savingsHistoryOut
	SavingsPointOutput   = savingsPointOut

	ListCategoriesOutput    = listCategoriesOut
	CategoryOutput          = categoryOut
	ListAccountsOutput      = listAccountsOut
	AccountOutput           = accountOut
	SnapshotOutput          = snapshotOut
	ListExchangeRatesOutput = listExchangeRatesOut
	RateOutput              = rateOut
	ListUsersOutput         = listUsersOut
	UserOutput              = userOut
	CreateUserOutput        = createUserOut
	DeleteOutput            = deleteOut
)
