package diff

import (
	"fmt"

	"github.com/stripe/pg-schema-diff/internal/schema"
)

var (
	migrationHazardPrivilegeGranted = MigrationHazard{
		Type:    MigrationHazardTypeAuthzUpdate,
		Message: "Granting privileges could allow unauthorized access to data.",
	}
	migrationHazardPrivilegeRevoked = MigrationHazard{
		Type:    MigrationHazardTypeAuthzUpdate,
		Message: "Revoking privileges could cause queries to fail if not correctly configured.",
	}
)

// privilegeGranteeSQL returns the SQL representation of a privilege grantee. An empty grantee means PUBLIC.
func privilegeGranteeSQL(grantee string) string {
	if grantee == "" {
		return "PUBLIC"
	}
	return schema.EscapeIdentifier(grantee)
}

type privilegeSQLVertexGenerator struct {
	objType   string
	objName   schema.SchemaQualifiedName
	sqlTarget string
}

// tablePrivilegeObjType is the vertex id prefix of a privilege on a relation.
const tablePrivilegeObjType = "privilege"

func newPrivilegeSQLVertexGenerator(tableName schema.SchemaQualifiedName) sqlVertexGenerator[schema.TablePrivilege, privilegeDiff] {
	return newPrivilegeSQLVertexGeneratorForObject(tablePrivilegeObjType, tableName, tableName.GetFQEscapedName())
}

func newPrivilegeSQLVertexGeneratorForObject(
	objType string,
	objName schema.SchemaQualifiedName,
	sqlTarget string,
) sqlVertexGenerator[schema.Privilege, privilegeDiff] {
	return legacyToNewSqlVertexGenerator[schema.Privilege, privilegeDiff](&privilegeSQLVertexGenerator{
		objType:   objType,
		objName:   objName,
		sqlTarget: sqlTarget,
	})
}

func newFunctionPrivilegeSQLVertexGenerator(functionName schema.SchemaQualifiedName) sqlVertexGenerator[schema.Privilege, privilegeDiff] {
	return newPrivilegeSQLVertexGeneratorForObject(
		"function_privilege",
		functionName,
		fmt.Sprintf("FUNCTION %s", functionName.GetFQEscapedName()),
	)
}

func newProcedurePrivilegeSQLVertexGenerator(procedureName schema.SchemaQualifiedName) sqlVertexGenerator[schema.Privilege, privilegeDiff] {
	return newPrivilegeSQLVertexGeneratorForObject(
		"procedure_privilege",
		procedureName,
		fmt.Sprintf("PROCEDURE %s", procedureName.GetFQEscapedName()),
	)
}

// typePrivilegeObjType is the vertex id prefix of a privilege on a type or domain.
const typePrivilegeObjType = "type_privilege"

func newTypePrivilegeSQLVertexGenerator(name schema.SchemaQualifiedName, objKind string) sqlVertexGenerator[schema.Privilege, privilegeDiff] {
	return newPrivilegeSQLVertexGeneratorForObject(
		typePrivilegeObjType,
		name,
		fmt.Sprintf("%s %s", objKind, name.GetFQEscapedName()),
	)
}

// generateTypePrivilegeStatements resolves the privilege diff of one type or domain through the
// shared privilege generator.
func generateTypePrivilegeStatements(
	name schema.SchemaQualifiedName,
	objKind string,
	diffs listDiff[schema.Privilege, privilegeDiff],
) ([]Statement, error) {
	generator := newTypePrivilegeSQLVertexGenerator(name, objKind)
	partialGraph, err := generatePartialGraph(generator, diffs)
	if err != nil {
		return nil, fmt.Errorf("resolving type privilege sql: %w", err)
	}
	statements, err := graphStatements(partialGraph)
	if err != nil {
		return nil, fmt.Errorf("ordering type privilege sql: %w", err)
	}
	return statements, nil
}

// classDefaultRoutineExecute is the EXECUTE grant PostgreSQL gives PUBLIC on a newly created
// function or procedure.
var classDefaultRoutineExecute = schema.Privilege{Privilege: "EXECUTE"}

// classDefaultTypeUsage is the USAGE grant PostgreSQL gives PUBLIC on a newly created type or
// domain.
var classDefaultTypeUsage = schema.Privilege{Privilege: "USAGE"}

// createTimePrivileges returns the ACL a newly created object of objectType in schemaName holds
// right after CREATE: the class defaults PostgreSQL attaches implicitly plus every schema-scoped
// default privilege the target schema declares for that object class and that applies to
// applyingRole. The plan emits the target's default privileges before it creates any object, so
// these are the grants the CREATE statement leaves behind. A rule scoped to another role does not
// take effect: ALTER DEFAULT PRIVILEGES FOR ROLE only changes what that role's CREATE leaves
// behind.
func createTimePrivileges(
	defaultPrivileges []schema.DefaultPrivilege,
	applyingRole, objectType, schemaName string,
	classDefaults ...schema.Privilege,
) []schema.Privilege {
	privileges := make([]schema.Privilege, 0, len(classDefaults)+len(defaultPrivileges))
	byName := make(map[string]int, len(classDefaults)+len(defaultPrivileges))
	add := func(p schema.Privilege) {
		if i, ok := byName[p.GetName()]; ok {
			// More than one rule can grant the same privilege to the same grantee; the
			// effective ACL carries the grant option if any of them does.
			privileges[i].IsGrantable = privileges[i].IsGrantable || p.IsGrantable
			return
		}
		byName[p.GetName()] = len(privileges)
		privileges = append(privileges, p)
	}
	for _, p := range classDefaults {
		add(p)
	}
	for _, p := range defaultPrivileges {
		if p.TargetRole != applyingRole || p.ObjectType != objectType || p.SchemaName != schemaName {
			continue
		}
		add(schema.Privilege{Grantee: p.Grantee, Privilege: p.Privilege, IsGrantable: p.IsGrantable})
	}
	return privileges
}

// exactPrivilegeStatements brings a newly created object from the ACL PostgreSQL leaves on it at
// CREATE (createTimePrivileges) to the target ACL: a create-time privilege the target does not
// hold is revoked after the create, and a target privilege the create-time ACL does not hold is
// granted. Hazards are stripped because the object did not exist before, so a revoke cannot break
// a caller already using it.
func exactPrivilegeStatements(
	generator sqlVertexGenerator[schema.Privilege, privilegeDiff],
	targetPrivileges, createPrivileges []schema.Privilege,
) ([]Statement, error) {
	privilegesDiff, err := diffLists(createPrivileges, targetPrivileges,
		func(old, new schema.Privilege, _, _ int) (privilegeDiff, bool, error) {
			// There is no ALTER for a grant option change: revoke the grant and grant it again.
			return privilegeDiff{oldAndNew[schema.Privilege]{old: old, new: new}},
				old.IsGrantable != new.IsGrantable, nil
		})
	if err != nil {
		return nil, fmt.Errorf("diffing the create-time privileges: %w", err)
	}

	var stmts []Statement
	for _, p := range privilegesDiff.deletes {
		deletePartialGraph, err := generator.Delete(p)
		if err != nil {
			return nil, fmt.Errorf("generating revoke statement for privilege %s: %w", p.GetName(), err)
		}
		stmts = append(stmts, stripMigrationHazards(deletePartialGraph.statements()...)...)
	}
	for _, p := range privilegesDiff.adds {
		addPartialGraph, err := generator.Add(p)
		if err != nil {
			return nil, fmt.Errorf("generating grant statement for privilege %s: %w", p.GetName(), err)
		}
		stmts = append(stmts, stripMigrationHazards(addPartialGraph.statements()...)...)
	}
	return stmts, nil
}

func (psg *privilegeSQLVertexGenerator) Add(p schema.Privilege) ([]Statement, error) {
	ddl := fmt.Sprintf("GRANT %s ON %s TO %s", p.Privilege, psg.sqlTarget, privilegeGranteeSQL(p.Grantee))
	if p.IsGrantable {
		ddl += " WITH GRANT OPTION"
	}

	return []Statement{{
		DDL:            ddl,
		Timeout:        statementTimeoutDefault,
		LockTimeout:    lockTimeoutDefault,
		Hazards:        []MigrationHazard{migrationHazardPrivilegeGranted},
		SkipValidation: true,
	}}, nil
}

func (psg *privilegeSQLVertexGenerator) Delete(p schema.Privilege) ([]Statement, error) {
	ddl := fmt.Sprintf("REVOKE %s ON %s FROM %s", p.Privilege, psg.sqlTarget, privilegeGranteeSQL(p.Grantee))

	return []Statement{{
		DDL:            ddl,
		Timeout:        statementTimeoutDefault,
		LockTimeout:    lockTimeoutDefault,
		Hazards:        []MigrationHazard{migrationHazardPrivilegeRevoked},
		SkipValidation: true,
	}}, nil
}

func (psg *privilegeSQLVertexGenerator) Alter(diff privilegeDiff) ([]Statement, error) {
	// Privileges don't support ALTER - if IsGrantable changes, we need to recreate
	// (handled via requiresRecreation in buildTableDiff)
	// This should not normally be called since only IsGrantable can change and that
	// triggers recreation.
	return nil, nil
}

func (psg *privilegeSQLVertexGenerator) GetSQLVertexId(p schema.Privilege, diffType diffType) sqlVertexId {
	return privilegeSQLVertexId(psg.objType, psg.objName, p, diffType)
}

// privilegeSQLVertexId names the vertex that carries the statements for one
// privilege. A generator that has to order its own statements against a
// privilege on another object builds the same id through this.
func privilegeSQLVertexId(objType string, objName schema.SchemaQualifiedName, p schema.Privilege, d diffType) sqlVertexId {
	return buildSchemaObjVertexId(objType, fmt.Sprintf("%s.%s", objName.GetFQEscapedName(), p.GetName()), d)
}

// tablePrivilegeRevokeVertexId names the vertex that carries the REVOKE of one
// privilege on a table. A table-level REVOKE removes that privilege from every
// column that holds it, so a column grant of the same privilege has to run after
// this vertex.
func tablePrivilegeRevokeVertexId(table schema.SchemaQualifiedName, p schema.Privilege) sqlVertexId {
	return privilegeSQLVertexId(tablePrivilegeObjType, table, p, diffTypeDelete)
}

func (psg *privilegeSQLVertexGenerator) GetAddAlterDependencies(newPriv, _ schema.Privilege) ([]dependency, error) {
	// Ensure delete runs before add/alter (for recreate scenarios)
	return []dependency{
		mustRun(psg.GetSQLVertexId(newPriv, diffTypeDelete)).before(psg.GetSQLVertexId(newPriv, diffTypeAddAlter)),
	}, nil
}

func (psg *privilegeSQLVertexGenerator) GetDeleteDependencies(_ schema.Privilege) ([]dependency, error) {
	return nil, nil
}
