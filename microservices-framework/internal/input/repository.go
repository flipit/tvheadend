package input

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"gorm.io/gorm"
)

// Repository interface for Input data access
type Repository interface {
	// Adapters
	CreateAdapter(ctx context.Context, adapter *Adapter) error
	GetAdapter(ctx context.Context, id string) (*Adapter, error)
	ListAdapters(ctx context.Context, filter *AdapterFilter) ([]*Adapter, string, int, error)
	UpdateAdapter(ctx context.Context, adapter *Adapter) error
	DeleteAdapter(ctx context.Context, id string) error

	// Inputs
	CreateInput(ctx context.Context, input *Input) error
	GetInput(ctx context.Context, id string) (*Input, error)
	ListInputs(ctx context.Context, filter *InputFilter) ([]*Input, string, int, error)
	UpdateInput(ctx context.Context, input *Input) error
	DeleteInput(ctx context.Context, id string) error

	// Muxes
	CreateMux(ctx context.Context, mux *Mux) error
	GetMux(ctx context.Context, id string) (*Mux, error)
	ListMuxes(ctx context.Context, filter *MuxFilter) ([]*Mux, string, int, error)
	UpdateMux(ctx context.Context, mux *Mux) error
	DeleteMux(ctx context.Context, id string) error

	// Services
	CreateService(ctx context.Context, service *Service) error
	GetService(ctx context.Context, id string) (*Service, error)
	ListServices(ctx context.Context, filter *ServiceFilter) ([]*Service, string, int, error)
	UpdateService(ctx context.Context, service *Service) error
	DeleteService(ctx context.Context, id string) error
}

// repository implements Repository using GORM
type repository struct {
	db *gorm.DB
}

// NewRepository creates a new repository
func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

// Adapter methods

func (r *repository) CreateAdapter(ctx context.Context, adapter *Adapter) error {
	return r.db.WithContext(ctx).Create(adapter).Error
}

func (r *repository) GetAdapter(ctx context.Context, id string) (*Adapter, error) {
	var adapter Adapter
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&adapter).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrAdapterNotFound
	}
	return &adapter, err
}

func (r *repository) ListAdapters(ctx context.Context, filter *AdapterFilter) ([]*Adapter, string, int, error) {
	query := r.db.WithContext(ctx).Model(&Adapter{})

	// Apply filters
	if filter.Type != nil {
		query = query.Where("type = ?", *filter.Type)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, "", 0, err
	}

	// Apply pagination
	pageSize := filter.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if filter.PageToken != "" {
		offset = decodePageToken(filter.PageToken)
	}

	query = query.Order("created_at DESC").Limit(pageSize).Offset(offset)

	// Execute query
	var adapters []*Adapter
	if err := query.Find(&adapters).Error; err != nil {
		return nil, "", 0, err
	}

	// Generate next page token
	nextToken := ""
	if offset+len(adapters) < int(total) {
		nextToken = encodePageToken(offset + pageSize)
	}

	return adapters, nextToken, int(total), nil
}

func (r *repository) UpdateAdapter(ctx context.Context, adapter *Adapter) error {
	return r.db.WithContext(ctx).Save(adapter).Error
}

func (r *repository) DeleteAdapter(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&Adapter{})
	if result.RowsAffected == 0 {
		return ErrAdapterNotFound
	}
	return result.Error
}

// Input methods

func (r *repository) CreateInput(ctx context.Context, input *Input) error {
	return r.db.WithContext(ctx).Create(input).Error
}

func (r *repository) GetInput(ctx context.Context, id string) (*Input, error) {
	var input Input
	err := r.db.WithContext(ctx).
		Preload("Adapter").
		Where("id = ?", id).
		First(&input).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrInputNotFound
	}
	return &input, err
}

func (r *repository) ListInputs(ctx context.Context, filter *InputFilter) ([]*Input, string, int, error) {
	query := r.db.WithContext(ctx).Model(&Input{}).Preload("Adapter")

	// Apply filters
	if filter.Type != nil {
		query = query.Where("type = ?", *filter.Type)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.EnabledOnly {
		query = query.Where("enabled = ?", true)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, "", 0, err
	}

	// Apply ordering
	orderBy := "priority DESC, created_at DESC"
	if filter.OrderBy != "" {
		orderBy = filter.OrderBy
	}
	query = query.Order(orderBy)

	// Apply pagination
	pageSize := filter.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if filter.PageToken != "" {
		offset = decodePageToken(filter.PageToken)
	}

	query = query.Limit(pageSize).Offset(offset)

	// Execute query
	var inputs []*Input
	if err := query.Find(&inputs).Error; err != nil {
		return nil, "", 0, err
	}

	// Generate next page token
	nextToken := ""
	if offset+len(inputs) < int(total) {
		nextToken = encodePageToken(offset + pageSize)
	}

	return inputs, nextToken, int(total), nil
}

func (r *repository) UpdateInput(ctx context.Context, input *Input) error {
	return r.db.WithContext(ctx).Save(input).Error
}

func (r *repository) DeleteInput(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&Input{})
	if result.RowsAffected == 0 {
		return ErrInputNotFound
	}
	return result.Error
}

// Mux methods

func (r *repository) CreateMux(ctx context.Context, mux *Mux) error {
	return r.db.WithContext(ctx).Create(mux).Error
}

func (r *repository) GetMux(ctx context.Context, id string) (*Mux, error) {
	var mux Mux
	err := r.db.WithContext(ctx).
		Preload("Input").
		Where("id = ?", id).
		First(&mux).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrMuxNotFound
	}
	return &mux, err
}

func (r *repository) ListMuxes(ctx context.Context, filter *MuxFilter) ([]*Mux, string, int, error) {
	query := r.db.WithContext(ctx).Model(&Mux{}).Preload("Input")

	// Apply filters
	if filter.InputID != "" {
		query = query.Where("input_id = ?", filter.InputID)
	}
	if filter.EnabledOnly {
		query = query.Where("enabled = ?", true)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, "", 0, err
	}

	// Apply pagination
	pageSize := filter.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if filter.PageToken != "" {
		offset = decodePageToken(filter.PageToken)
	}

	query = query.Order("frequency ASC").Limit(pageSize).Offset(offset)

	// Execute query
	var muxes []*Mux
	if err := query.Find(&muxes).Error; err != nil {
		return nil, "", 0, err
	}

	// Generate next page token
	nextToken := ""
	if offset+len(muxes) < int(total) {
		nextToken = encodePageToken(offset + pageSize)
	}

	return muxes, nextToken, int(total), nil
}

func (r *repository) UpdateMux(ctx context.Context, mux *Mux) error {
	return r.db.WithContext(ctx).Save(mux).Error
}

func (r *repository) DeleteMux(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&Mux{})
	if result.RowsAffected == 0 {
		return ErrMuxNotFound
	}
	return result.Error
}

// Service methods

func (r *repository) CreateService(ctx context.Context, service *Service) error {
	return r.db.WithContext(ctx).Create(service).Error
}

func (r *repository) GetService(ctx context.Context, id string) (*Service, error) {
	var service Service
	err := r.db.WithContext(ctx).
		Preload("Mux").
		Preload("Mux.Input").
		Where("id = ?", id).
		First(&service).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrServiceNotFound
	}
	return &service, err
}

func (r *repository) ListServices(ctx context.Context, filter *ServiceFilter) ([]*Service, string, int, error) {
	query := r.db.WithContext(ctx).Model(&Service{}).Preload("Mux")

	// Apply filters
	if filter.MuxID != "" {
		query = query.Where("mux_id = ?", filter.MuxID)
	}
	if filter.InputID != "" {
		// Join with muxes table to filter by input_id
		query = query.Joins("JOIN muxes ON muxes.id = services.mux_id").
			Where("muxes.input_id = ?", filter.InputID)
	}
	if filter.Type != nil {
		query = query.Where("type = ?", *filter.Type)
	}
	if filter.EnabledOnly {
		query = query.Where("enabled = ?", true)
	}
	if filter.ExcludeEncrypted {
		query = query.Where("encrypted = ?", false)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, "", 0, err
	}

	// Apply pagination
	pageSize := filter.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if filter.PageToken != "" {
		offset = decodePageToken(filter.PageToken)
	}

	query = query.Order("name ASC").Limit(pageSize).Offset(offset)

	// Execute query
	var services []*Service
	if err := query.Find(&services).Error; err != nil {
		return nil, "", 0, err
	}

	// Generate next page token
	nextToken := ""
	if offset+len(services) < int(total) {
		nextToken = encodePageToken(offset + pageSize)
	}

	return services, nextToken, int(total), nil
}

func (r *repository) UpdateService(ctx context.Context, service *Service) error {
	return r.db.WithContext(ctx).Save(service).Error
}

func (r *repository) DeleteService(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&Service{})
	if result.RowsAffected == 0 {
		return ErrServiceNotFound
	}
	return result.Error
}

// AutoMigrate runs database migrations
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Adapter{},
		&Input{},
		&Mux{},
		&Service{},
	)
}

// Pagination helpers

type pageToken struct {
	Offset int `json:"offset"`
}

func encodePageToken(offset int) string {
	token := pageToken{Offset: offset}
	data, _ := json.Marshal(token)
	return base64.StdEncoding.EncodeToString(data)
}

func decodePageToken(token string) int {
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}

	var pt pageToken
	if err := json.Unmarshal(data, &pt); err != nil {
		return 0
	}

	return pt.Offset
}
