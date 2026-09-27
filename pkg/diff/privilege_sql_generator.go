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

func exactRoutinePrivilegeStatements(
	generator sqlVertexGenerator[schema.Privilege, privilegeDiff],
	privileges []schema.Privilege,
) ([]Statement, error) {
	var stmts []Statement
	hasDefaultPublicExecute := false
	for _, p := range privileges {
		if p.Grantee == "" && p.Privilege == "EXECUTE" && !p.IsGrantable {
			hasDefaultPublicExecute = true
			continue
		}
		addPartialGraph, err := generator.Add(p)
		if err != nil {
			return nil, fmt.Errorf("generating grant statement for privilege %s: %w", p.GetName(), err)
		}
		stmts = append(stmts, stripMigrationHazards(addPartialGraph.statements()...)...)
	}
	if !hasDefaultPublicExecute {
		deletePartialGraph, err := generator.Delete(schema.Privilege{Privilege: "EXECUTE"})
		if err != nil {
			return nil, fmt.Errorf("generating revoke statement for default PUBLIC EXECUTE: %w", err)
		}
		stmts = append(stripMigrationHazards(deletePartialGraph.statements()...), stmts...)
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
