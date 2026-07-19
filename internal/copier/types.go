package copier

type tableMeta struct {
	Schema            string
	Name              string
	ObjectID          int
	ApproxRows        int64
	DependsOn         []string
	Columns           []columnMeta
	CopyColumns       []columnMeta
	PrimaryKey        *keyConstraint
	Checks            []checkConstraint
	ForeignKeys       []foreignKey
	Indexes           []indexMeta
	HasIdentity       bool
	BulkOK            bool
	BulkReason        string
	DependencyOnly    bool
	TargetPreexisting bool
}

type columnMeta struct {
	Name               string
	TypeSchema         string
	UserTypeName       string
	SystemTypeName     string
	IsUserDefined      bool
	MaxLength          int
	Precision          int
	Scale              int
	Nullable           bool
	Identity           bool
	IdentitySeed       string
	IdentityIncrement  string
	Computed           bool
	ComputedDefinition string
	ComputedPersisted  bool
	DefaultDefinition  string
	Collation          string
	RowGuidCol         bool
	Sparse             bool
	Hidden             bool
	GeneratedAlways    int
	Copyable           bool
	SkipReason         string
}

type keyConstraint struct {
	Name    string
	Kind    string
	Cluster string
	Columns []keyColumn
}

type keyColumn struct {
	Name string
	Desc bool
}

type checkConstraint struct {
	Name       string
	Definition string
	Trusted    bool
	Disabled   bool
}

type foreignKey struct {
	Name         string
	Columns      []string
	RefSchema    string
	RefTable     string
	RefColumns   []string
	DeleteAction string
	UpdateAction string
	Trusted      bool
	Disabled     bool
}

type indexMeta struct {
	Name       string
	Unique     bool
	Cluster    string
	Filter     string
	KeyColumns []keyColumn
	Include    []string
}

type viewMeta struct {
	Schema     string
	Name       string
	Definition string
	DependsOn  []string
}

type sequenceMeta struct {
	Schema      string
	Name        string
	TypeName    string
	Precision   int
	Scale       int
	StartValue  string
	Increment   string
	MinValue    string
	MaxValue    string
	RestartWith string
	IsCycling   bool
	IsCached    bool
	CacheSize   int64
}

type procedureMeta struct {
	Schema     string
	Name       string
	Definition string
	DependsOn  []string
}

type functionMeta struct {
	Schema     string
	Name       string
	Kind       string
	Definition string
	DependsOn  []string
}

type synonymMeta struct {
	Schema         string
	Name           string
	BaseObjectName string
}

type aliasTypeMeta struct {
	Schema         string
	Name           string
	SystemTypeName string
	MaxLength      int
	Precision      int
	Scale          int
	Nullable       bool
}

type tableTypeMeta struct {
	Schema     string
	Name       string
	ObjectID   int
	Columns    []columnMeta
	PrimaryKey *keyConstraint
	Checks     []checkConstraint
}

type triggerMeta struct {
	Schema      string
	Name        string
	TableSchema string
	TableName   string
	Definition  string
	Disabled    bool
	DependsOn   []string
}
