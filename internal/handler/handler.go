package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/walker-qiang/personal-finance/internal/db/store"
	"github.com/walker-qiang/personal-finance/internal/publish"
)

type API struct {
	Store *store.Store
	Job   *publish.Job
}

func (a *API) Register(r *gin.Engine) {
	r.GET("/healthz", a.healthz)

	g := r.Group("/api/finance")
	{
		// Reads
		g.GET("/assets", a.listAssets)
		g.GET("/assets/:id", a.getAsset)
		g.GET("/snapshots", a.listSnapshots)
		g.GET("/snapshots/:id", a.getSnapshot)
		g.GET("/transactions", a.listTransactions)
		g.GET("/transactions/:id", a.getTransaction)
		g.GET("/holdings", a.listHoldings)

		// Mutations
		g.POST("/assets", a.upsertAsset)
		g.PATCH("/assets/:id", a.patchAsset)
		g.DELETE("/assets/:id", a.archiveAsset)

		g.POST("/snapshots", a.upsertSnapshot)
		g.PATCH("/snapshots/:id", a.patchSnapshot)
		g.DELETE("/snapshots/:id", a.deleteSnapshot)

		g.POST("/transactions", a.createTransaction)
		g.PATCH("/transactions/:id", a.patchTransaction)
		g.DELETE("/transactions/:id", a.deleteTransaction)

		// Bucket targets (target weights for the cash / stable / growth split)
		g.GET("/bucket-targets", a.listBucketTargets)
		g.PUT("/bucket-targets", a.upsertBucketTarget)
		g.DELETE("/bucket-targets/:bucket", a.deleteBucketTarget)

		// Publish
		g.POST("/publish", a.runPublish)
		g.GET("/publish/last", a.lastPublish)
	}
}

func (a *API) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"ts": time.Now().UTC().Format(time.RFC3339),
	})
}

// ---------- helpers ----------

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must be a positive integer"})
		return 0, false
	}
	return id, true
}

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func internalErr(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func notFound(c *gin.Context, what string) {
	c.JSON(http.StatusNotFound, gin.H{"error": what + " not found"})
}

// ---------- assets: read ----------

func (a *API) listAssets(c *gin.Context) {
	f := store.AssetFilter{
		Bucket:          c.Query("bucket"),
		AssetType:       c.Query("asset_type"),
		IncludeArchived: c.Query("include_archived") == "1" || c.Query("include_archived") == "true",
	}
	if f.Bucket != "" {
		if err := validateBucket(f.Bucket); err != nil {
			badRequest(c, err)
			return
		}
	}
	if f.AssetType != "" {
		if err := validateAssetType(f.AssetType); err != nil {
			badRequest(c, err)
			return
		}
	}
	rows, err := a.Store.ListAssetsFiltered(c.Request.Context(), f)
	if err != nil {
		internalErr(c, err)
		return
	}
	out := toAssetsResp(rows)
	c.JSON(http.StatusOK, gin.H{"assets": out, "count": len(out)})
}

func (a *API) getAsset(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	row, err := a.Store.GetAssetByID(c.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(c, "asset")
		return
	}
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"asset": toAssetResp(row)})
}

// ---------- assets: write ----------

type upsertAssetReq struct {
	Code             string   `json:"code"`
	Name             string   `json:"name"`
	AssetType        string   `json:"asset_type"`
	Bucket           string   `json:"bucket"`
	Channel          string   `json:"channel"`
	Currency         string   `json:"currency"`
	RiskLevel        string   `json:"risk_level"` // "" → NULL
	HoldingCostPct   *float64 `json:"holding_cost_pct"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct"`
	Notes            string   `json:"notes"`
}

func (a *API) upsertAsset(c *gin.Context) {
	var req upsertAssetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if req.Code == "" || req.Name == "" {
		badRequest(c, errors.New("code and name are required"))
		return
	}
	if err := validateAssetType(req.AssetType); err != nil {
		badRequest(c, err)
		return
	}
	if err := validateBucket(req.Bucket); err != nil {
		badRequest(c, err)
		return
	}
	if req.Currency == "" {
		req.Currency = "CNY"
	}
	if err := validateCurrency(req.Currency); err != nil {
		badRequest(c, err)
		return
	}
	if err := validateRiskLevel(req.RiskLevel); err != nil {
		badRequest(c, err)
		return
	}

	asset := store.Asset{
		Code:      req.Code,
		Name:      req.Name,
		AssetType: req.AssetType,
		Bucket:    req.Bucket,
		Channel:   req.Channel,
		Currency:  req.Currency,
		Notes:     req.Notes,
	}
	if req.RiskLevel != "" {
		asset.RiskLevel = sql.NullString{String: req.RiskLevel, Valid: true}
	}
	if req.HoldingCostPct != nil {
		asset.HoldingCostPct = sql.NullFloat64{Float64: *req.HoldingCostPct, Valid: true}
	}
	if req.ExpectedYieldPct != nil {
		asset.ExpectedYieldPct = sql.NullFloat64{Float64: *req.ExpectedYieldPct, Valid: true}
	}

	id, err := a.Store.UpsertAsset(c.Request.Context(), asset)
	if err != nil {
		internalErr(c, err)
		return
	}
	row, err := a.Store.GetAssetByID(c.Request.Context(), id)
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"asset": toAssetResp(row)})
}

type patchAssetReq struct {
	Name             *string  `json:"name"`
	AssetType        *string  `json:"asset_type"`
	Bucket           *string  `json:"bucket"`
	Channel          *string  `json:"channel"`
	Currency         *string  `json:"currency"`
	RiskLevel        *string  `json:"risk_level"` // empty string clears
	HoldingCostPct   *float64 `json:"holding_cost_pct"`
	ClearHoldingCost bool     `json:"clear_holding_cost_pct"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct"`
	ClearExpectedY   bool     `json:"clear_expected_yield_pct"`
	Notes            *string  `json:"notes"`
}

func (a *API) patchAsset(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req patchAssetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if req.AssetType != nil {
		if err := validateAssetType(*req.AssetType); err != nil {
			badRequest(c, err)
			return
		}
	}
	if req.Bucket != nil {
		if err := validateBucket(*req.Bucket); err != nil {
			badRequest(c, err)
			return
		}
	}
	if req.Currency != nil {
		if err := validateCurrency(*req.Currency); err != nil {
			badRequest(c, err)
			return
		}
	}
	if req.RiskLevel != nil {
		if err := validateRiskLevel(*req.RiskLevel); err != nil {
			badRequest(c, err)
			return
		}
	}

	patch := store.AssetPatch{
		Name:             req.Name,
		AssetType:        req.AssetType,
		Bucket:           req.Bucket,
		Channel:          req.Channel,
		Currency:         req.Currency,
		RiskLevel:        req.RiskLevel,
		HoldingCostPct:   req.HoldingCostPct,
		ClearHoldingCost: req.ClearHoldingCost,
		ExpectedYieldPct: req.ExpectedYieldPct,
		ClearExpectedY:   req.ClearExpectedY,
		Notes:            req.Notes,
	}
	if err := a.Store.PatchAsset(c.Request.Context(), id, patch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "asset")
			return
		}
		internalErr(c, err)
		return
	}
	row, err := a.Store.GetAssetByID(c.Request.Context(), id)
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"asset": toAssetResp(row)})
}

func (a *API) archiveAsset(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := a.Store.ArchiveAsset(c.Request.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "asset")
			return
		}
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"archived_id": id})
}

// ---------- snapshots: read ----------

func (a *API) listSnapshots(c *gin.Context) {
	f := store.SnapshotFilter{
		Since: c.Query("since"),
		Until: c.Query("until"),
	}
	if v := c.Query("asset_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			badRequest(c, errors.New("asset_id must be a positive integer"))
			return
		}
		f.AssetID = id
	}
	if f.Since != "" {
		if err := validateDate(f.Since); err != nil {
			badRequest(c, err)
			return
		}
	}
	if f.Until != "" {
		if err := validateDate(f.Until); err != nil {
			badRequest(c, err)
			return
		}
	}
	rows, err := a.Store.ListSnapshotsFiltered(c.Request.Context(), f)
	if err != nil {
		internalErr(c, err)
		return
	}
	out := toSnapshotsResp(rows)
	c.JSON(http.StatusOK, gin.H{"snapshots": out, "count": len(out)})
}

func (a *API) getSnapshot(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	row, err := a.Store.GetSnapshotByID(c.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(c, "snapshot")
		return
	}
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"snapshot": toSnapshotResp(row)})
}

// ---------- snapshots: write ----------

type upsertSnapshotReq struct {
	AssetID          int64    `json:"asset_id"`
	AssetCode        string   `json:"asset_code"` // alternative to asset_id
	SnapshotDate     string   `json:"snapshot_date"`
	BalanceCents     *int64   `json:"balance_cents"`
	BalanceYuan      *float64 `json:"balance_yuan"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct"`
	ActualYieldPct   *float64 `json:"actual_yield_pct"`
	Notes            string   `json:"notes"`
}

func (a *API) upsertSnapshot(c *gin.Context) {
	var req upsertSnapshotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if req.AssetID == 0 && req.AssetCode == "" {
		badRequest(c, errors.New("asset_id or asset_code is required"))
		return
	}
	if req.AssetID == 0 {
		id, err := a.Store.GetAssetIDByCode(c.Request.Context(), req.AssetCode)
		if errors.Is(err, sql.ErrNoRows) {
			badRequest(c, errors.New("asset_code "+req.AssetCode+" not found"))
			return
		}
		if err != nil {
			internalErr(c, err)
			return
		}
		req.AssetID = id
	}
	if err := validateDate(req.SnapshotDate); err != nil {
		badRequest(c, err)
		return
	}
	cents, err := resolveCents(req.BalanceCents, req.BalanceYuan)
	if err != nil {
		badRequest(c, err)
		return
	}
	if err := validateNonNegativeCents("balance_cents", cents); err != nil {
		badRequest(c, err)
		return
	}

	sn := store.Snapshot{
		AssetID:      req.AssetID,
		SnapshotDate: req.SnapshotDate,
		BalanceCents: cents,
		Notes:        req.Notes,
	}
	if req.ExpectedYieldPct != nil {
		sn.ExpectedYieldPct = sql.NullFloat64{Float64: *req.ExpectedYieldPct, Valid: true}
	}
	if req.ActualYieldPct != nil {
		sn.ActualYieldPct = sql.NullFloat64{Float64: *req.ActualYieldPct, Valid: true}
	}

	id, err := a.Store.UpsertSnapshot(c.Request.Context(), sn)
	if err != nil {
		internalErr(c, err)
		return
	}
	row, err := a.Store.GetSnapshotByID(c.Request.Context(), id)
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"snapshot": toSnapshotResp(row)})
}

type patchSnapshotReq struct {
	BalanceCents     *int64   `json:"balance_cents"`
	BalanceYuan      *float64 `json:"balance_yuan"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct"`
	ClearExpectedY   bool     `json:"clear_expected_yield_pct"`
	ActualYieldPct   *float64 `json:"actual_yield_pct"`
	ClearActualY     bool     `json:"clear_actual_yield_pct"`
	Notes            *string  `json:"notes"`
}

func (a *API) patchSnapshot(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req patchSnapshotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	patch := store.SnapshotPatch{
		ExpectedYieldPct: req.ExpectedYieldPct,
		ClearExpectedY:   req.ClearExpectedY,
		ActualYieldPct:   req.ActualYieldPct,
		ClearActualY:     req.ClearActualY,
		Notes:            req.Notes,
	}
	if req.BalanceCents != nil || req.BalanceYuan != nil {
		cents, err := resolveCents(req.BalanceCents, req.BalanceYuan)
		if err != nil {
			badRequest(c, err)
			return
		}
		if err := validateNonNegativeCents("balance_cents", cents); err != nil {
			badRequest(c, err)
			return
		}
		patch.BalanceCents = &cents
	}
	if err := a.Store.PatchSnapshot(c.Request.Context(), id, patch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "snapshot")
			return
		}
		internalErr(c, err)
		return
	}
	row, err := a.Store.GetSnapshotByID(c.Request.Context(), id)
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"snapshot": toSnapshotResp(row)})
}

func (a *API) deleteSnapshot(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := a.Store.DeleteSnapshot(c.Request.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "snapshot")
			return
		}
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted_id": id})
}

// ---------- transactions: read ----------

func (a *API) listTransactions(c *gin.Context) {
	f := store.TransactionFilter{
		Direction: c.Query("direction"),
		Since:     c.Query("since"),
		Until:     c.Query("until"),
	}
	if v := c.Query("asset_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			badRequest(c, errors.New("asset_id must be a positive integer"))
			return
		}
		f.AssetID = id
	}
	if f.Direction != "" {
		if err := validateDirection(f.Direction); err != nil {
			badRequest(c, err)
			return
		}
	}
	if f.Since != "" {
		if err := validateDate(f.Since); err != nil {
			badRequest(c, err)
			return
		}
	}
	if f.Until != "" {
		if err := validateDate(f.Until); err != nil {
			badRequest(c, err)
			return
		}
	}
	rows, err := a.Store.ListTransactionsFiltered(c.Request.Context(), f)
	if err != nil {
		internalErr(c, err)
		return
	}
	out := toTransactionsResp(rows)
	c.JSON(http.StatusOK, gin.H{"transactions": out, "count": len(out)})
}

func (a *API) getTransaction(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	row, err := a.Store.GetTransactionByID(c.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(c, "transaction")
		return
	}
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"transaction": toTransactionResp(row)})
}

// ---------- transactions: write ----------

type createTxnReq struct {
	AssetID     int64    `json:"asset_id"`
	AssetCode   string   `json:"asset_code"`
	TxnDate     string   `json:"txn_date"`
	Direction   string   `json:"direction"`
	AmountCents *int64   `json:"amount_cents"`
	AmountYuan  *float64 `json:"amount_yuan"`
	FeeCents    *int64   `json:"fee_cents"`
	FeeYuan     *float64 `json:"fee_yuan"`
	Notes       string   `json:"notes"`
}

func (a *API) createTransaction(c *gin.Context) {
	var req createTxnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if req.AssetID == 0 && req.AssetCode == "" {
		badRequest(c, errors.New("asset_id or asset_code is required"))
		return
	}
	if req.AssetID == 0 {
		id, err := a.Store.GetAssetIDByCode(c.Request.Context(), req.AssetCode)
		if errors.Is(err, sql.ErrNoRows) {
			badRequest(c, errors.New("asset_code "+req.AssetCode+" not found"))
			return
		}
		if err != nil {
			internalErr(c, err)
			return
		}
		req.AssetID = id
	}
	if err := validateDate(req.TxnDate); err != nil {
		badRequest(c, err)
		return
	}
	if err := validateDirection(req.Direction); err != nil {
		badRequest(c, err)
		return
	}
	amount, err := resolveCents(req.AmountCents, req.AmountYuan)
	if err != nil {
		badRequest(c, err)
		return
	}
	if err := validateNonNegativeCents("amount_cents", amount); err != nil {
		badRequest(c, err)
		return
	}
	var fee int64
	if req.FeeCents != nil || req.FeeYuan != nil {
		fee, err = resolveCents(req.FeeCents, req.FeeYuan)
		if err != nil {
			badRequest(c, err)
			return
		}
		if err := validateNonNegativeCents("fee_cents", fee); err != nil {
			badRequest(c, err)
			return
		}
	}

	id, err := a.Store.InsertTransaction(c.Request.Context(), store.Transaction{
		AssetID:     req.AssetID,
		TxnDate:     req.TxnDate,
		Direction:   req.Direction,
		AmountCents: amount,
		FeeCents:    fee,
		Notes:       req.Notes,
	})
	if err != nil {
		internalErr(c, err)
		return
	}
	row, err := a.Store.GetTransactionByID(c.Request.Context(), id)
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"transaction": toTransactionResp(row)})
}

type patchTxnReq struct {
	TxnDate     *string  `json:"txn_date"`
	Direction   *string  `json:"direction"`
	AmountCents *int64   `json:"amount_cents"`
	AmountYuan  *float64 `json:"amount_yuan"`
	FeeCents    *int64   `json:"fee_cents"`
	FeeYuan     *float64 `json:"fee_yuan"`
	Notes       *string  `json:"notes"`
}

func (a *API) patchTransaction(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req patchTxnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if req.TxnDate != nil {
		if err := validateDate(*req.TxnDate); err != nil {
			badRequest(c, err)
			return
		}
	}
	if req.Direction != nil {
		if err := validateDirection(*req.Direction); err != nil {
			badRequest(c, err)
			return
		}
	}
	patch := store.TransactionPatch{
		TxnDate:   req.TxnDate,
		Direction: req.Direction,
		Notes:     req.Notes,
	}
	if req.AmountCents != nil || req.AmountYuan != nil {
		v, err := resolveCents(req.AmountCents, req.AmountYuan)
		if err != nil {
			badRequest(c, err)
			return
		}
		if err := validateNonNegativeCents("amount_cents", v); err != nil {
			badRequest(c, err)
			return
		}
		patch.AmountCents = &v
	}
	if req.FeeCents != nil || req.FeeYuan != nil {
		v, err := resolveCents(req.FeeCents, req.FeeYuan)
		if err != nil {
			badRequest(c, err)
			return
		}
		if err := validateNonNegativeCents("fee_cents", v); err != nil {
			badRequest(c, err)
			return
		}
		patch.FeeCents = &v
	}
	if err := a.Store.PatchTransaction(c.Request.Context(), id, patch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "transaction")
			return
		}
		internalErr(c, err)
		return
	}
	row, err := a.Store.GetTransactionByID(c.Request.Context(), id)
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"transaction": toTransactionResp(row)})
}

func (a *API) deleteTransaction(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := a.Store.DeleteTransaction(c.Request.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "transaction")
			return
		}
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted_id": id})
}

// ---------- holdings ----------

func (a *API) listHoldings(c *gin.Context) {
	rows, err := a.Store.ListHoldings(c.Request.Context())
	if err != nil {
		internalErr(c, err)
		return
	}
	out := toHoldingsResp(rows)
	c.JSON(http.StatusOK, gin.H{"holdings": out, "count": len(out)})
}

// ---------- bucket-targets ----------

// listBucketTargets always returns 3 entries (cash / stable / growth) so the
// UI can render an empty-state row without doing a "missing bucket" check.
// Buckets with no row in the DB come back with target_pct=null, is_set=false.
func (a *API) listBucketTargets(c *gin.Context) {
	rows, err := a.Store.ListBucketTargets(c.Request.Context())
	if err != nil {
		internalErr(c, err)
		return
	}
	have := map[string]BucketTargetResp{}
	for _, r := range rows {
		have[r.Bucket] = toBucketTargetResp(r)
	}
	out := make([]BucketTargetResp, 0, 3)
	for _, b := range []string{"cash", "stable", "growth"} {
		if r, ok := have[b]; ok {
			out = append(out, r)
		} else {
			out = append(out, BucketTargetResp{Bucket: b, IsSet: false})
		}
	}

	// Compute sum across set targets so the UI can warn when the user has
	// over- or under-allocated. nil in the response means "no targets set
	// yet at all" (different from sum=0 which would be "all 3 set to 0").
	var sumPct *float64
	if len(rows) > 0 {
		var s float64
		for _, r := range rows {
			s += r.TargetPct
		}
		sumPct = &s
	}

	c.JSON(http.StatusOK, gin.H{
		"bucket_targets": out,
		"sum_pct":        sumPct,
	})
}

type upsertBucketTargetReq struct {
	Bucket    string  `json:"bucket"`
	TargetPct float64 `json:"target_pct"`
	Notes     string  `json:"notes"`
}

func (a *API) upsertBucketTarget(c *gin.Context) {
	var req upsertBucketTargetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if err := validateBucket(req.Bucket); err != nil {
		badRequest(c, err)
		return
	}
	if req.TargetPct < 0 || req.TargetPct > 100 {
		badRequest(c, errors.New("target_pct must be in [0, 100]"))
		return
	}
	row, err := a.Store.UpsertBucketTarget(c.Request.Context(), store.BucketTarget{
		Bucket:    req.Bucket,
		TargetPct: req.TargetPct,
		Notes:     req.Notes,
	})
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"bucket_target": toBucketTargetResp(row)})
}

func (a *API) deleteBucketTarget(c *gin.Context) {
	bucket := c.Param("bucket")
	if err := validateBucket(bucket); err != nil {
		badRequest(c, err)
		return
	}
	if err := a.Store.DeleteBucketTarget(c.Request.Context(), bucket); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "bucket_target")
			return
		}
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted_bucket": bucket})
}

// ---------- publish ----------

func (a *API) runPublish(c *gin.Context) {
	res := a.Job.Run(c.Request.Context())
	status := http.StatusOK
	if !res.OK {
		status = http.StatusInternalServerError
	}
	c.JSON(status, res)
}

// lastPublish reports the most recent auto-publish commit by scanning the
// publish worktree's git log. We deliberately don't track it in a separate DB
// table because git already is the source of truth — anything else would
// risk drift if the worktree gets reset/rebuilt manually.
//
// Behaviour:
//   - never block on the network: we use `git log` which is local-only.
//   - if no `[auto-publish]` commit exists yet (fresh worktree), return
//     `{"last": null}` instead of 404 so the UI renders "no publish yet".
//   - if the worktree itself is missing/broken, return 500 with the error.
func (a *API) lastPublish(c *gin.Context) {
	res, err := a.Job.LastPublish(c.Request.Context())
	if err != nil {
		internalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"last": res})
}
