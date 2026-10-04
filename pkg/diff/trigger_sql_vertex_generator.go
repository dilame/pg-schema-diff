package diff

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

type triggerDiff struct {
	oldAndNew[schema.Trigger]
}

type triggerSQLVertexGenerator struct {
	// functionsInNewSchemaByName is a map of function new to functions in the new schema.
	// These functions are not necessarily new
	functionsInNewSchemaByName map[string]schema.Function
}

func newTriggerSqlVertexGenerator(functionsInNewSchemaByName map[string]schema.Function) sqlVertexGenerator[schema.Trigger, triggerDiff] {
	return legacyToNewSqlVertexGenerator[schema.Trigger, triggerDiff](&triggerSQLVertexGenerator{
		functionsInNewSchemaByName: functionsInNewSchemaByName,
	})
}

func (t *triggerSQLVertexGenerator) Add(trigger schema.Trigger) ([]Statement, error) {
	return t.addStatements(trigger), nil
}

func (t *triggerSQLVertexGenerator) addStatements(trigger schema.Trigger) []Statement {
	stmts := []Statement{{
		DDL:         string(trigger.GetTriggerDefStmt),
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
	}}
	stmts = append(stmts, triggerEnabledStatements(trigger, triggerEnabledOnCreate)...)
	stmts = append(stmts, commentDDLForAdd(commentTargetTrigger(trigger.EscapedName, trigger.OwningTable), trigger.Description)...)
	return stmts
}

// triggerEnabledOnCreate is the state CREATE TRIGGER gives a trigger: it fires in origin and local
// mode.
const triggerEnabledOnCreate = "O"

// triggerEnabledStatements brings a trigger from the enabled state it has to the one it declares.
// An empty state is one the schema does not carry, and is left alone.
func triggerEnabledStatements(trigger schema.Trigger, current string) []Statement {
	if trigger.Enabled == "" || trigger.Enabled == current {
		return nil
	}
	var action string
	switch trigger.Enabled {
	case "O":
		action = "ENABLE TRIGGER"
	case "D":
		action = "DISABLE TRIGGER"
	case "R":
		action = "ENABLE REPLICA TRIGGER"
	case "A":
		action = "ENABLE ALWAYS TRIGGER"
	default:
		return nil
	}
	return []Statement{{
		DDL:         fmt.Sprintf("%s %s %s", alterTablePrefix(trigger.OwningTable), action, trigger.EscapedName),
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
	}}
}

func (t *triggerSQLVertexGenerator) Delete(trigger schema.Trigger) ([]Statement, error) {
	return t.deleteStatements(trigger), nil
}

func (t *triggerSQLVertexGenerator) deleteStatements(trigger schema.Trigger) []Statement {
	return []Statement{{
		DDL:         fmt.Sprintf("DROP TRIGGER %s ON %s", trigger.EscapedName, trigger.OwningTable.GetFQEscapedName()),
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
	}}
}

func (t *triggerSQLVertexGenerator) Alter(diff triggerDiff) ([]Statement, error) {
	if cmp.Equal(diff.old, diff.new) {
		return nil, nil
	}

	// Comment- and state-only diff: don't recreate the trigger, just emit the COMMENT and the
	// ENABLE / DISABLE statements. The functions it calls follow from its definition.
	oldCopy := diff.old
	oldCopy.Description = diff.new.Description
	oldCopy.Enabled = diff.new.Enabled
	oldCopy.DependsOnFunctions = diff.new.DependsOnFunctions
	if cmp.Equal(oldCopy, diff.new) {
		stmts := triggerEnabledStatements(diff.new, diff.old.Enabled)
		return append(stmts, commentDDLForAlter(commentTargetTrigger(diff.new.EscapedName, diff.new.OwningTable), diff.old.Description, diff.new.Description)...), nil
	}

	if diff.old.IsConstraint || diff.new.IsConstraint {
		// Constraint triggers do not support "CREATE OR REPLACE", so just drop the original trigger and
		// create the new one.
		return append(t.deleteStatements(diff.old), t.addStatements(diff.new)...), nil

	}

	createOrReplaceStmt, err := diff.new.GetTriggerDefStmt.ToCreateOrReplace()
	if err != nil {
		return nil, fmt.Errorf("modifying get trigger def statement to create or replace: %w", err)
	}
	stmts := []Statement{{
		DDL:         createOrReplaceStmt,
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
	}}
	stmts = append(stmts, triggerEnabledStatements(diff.new, diff.old.Enabled)...)
	stmts = append(stmts, commentDDLForAlter(commentTargetTrigger(diff.new.EscapedName, diff.new.OwningTable), diff.old.Description, diff.new.Description)...)
	return stmts, nil
}

func (t *triggerSQLVertexGenerator) GetSQLVertexId(trigger schema.Trigger, diffType diffType) sqlVertexId {
	return buildSchemaObjVertexId("trigger", trigger.GetName(), diffType)
}

func (t *triggerSQLVertexGenerator) GetAddAlterDependencies(newTrigger, oldTrigger schema.Trigger) ([]dependency, error) {
	// Since a trigger can just be `CREATE OR REPLACE`, there will never be a case where a trigger is
	// added and dropped in the same migration. Thus, we don't need a dependency on the delete node of a function
	// because there won't be one if it is being added/altered
	deps := []dependency{
		mustRun(t.GetSQLVertexId(newTrigger, diffTypeAddAlter)).after(buildFunctionVertexId(newTrigger.Function, diffTypeAddAlter)),
		mustRun(t.GetSQLVertexId(newTrigger, diffTypeAddAlter)).after(buildTableVertexId(newTrigger.OwningTable, diffTypeAddAlter)),
	}
	// Run after the functions its WHEN condition calls exist.
	for _, f := range newTrigger.DependsOnFunctions {
		deps = append(deps, mustRun(t.GetSQLVertexId(newTrigger, diffTypeAddAlter)).after(buildFunctionVertexId(f, diffTypeAddAlter)))
	}

	if !cmp.Equal(oldTrigger, schema.Trigger{}) {
		// If the trigger is being altered:
		// If the old version of the trigger called a function being deleted, the function deletion must come after the
		// trigger is altered, so the trigger no longer has a dependency on the function
		deps = append(deps,
			mustRun(t.GetSQLVertexId(newTrigger, diffTypeAddAlter)).before(buildFunctionVertexId(oldTrigger.Function, diffTypeDelete)),
		)
		newFunctions := make(map[string]bool)
		for _, f := range newTrigger.DependsOnFunctions {
			newFunctions[f.GetName()] = true
		}
		for _, f := range oldTrigger.DependsOnFunctions {
			if !newFunctions[f.GetName()] {
				deps = append(deps, mustRun(t.GetSQLVertexId(newTrigger, diffTypeAddAlter)).before(buildFunctionVertexId(f, diffTypeDelete)))
			}
		}
	}

	return deps, nil
}

func (t *triggerSQLVertexGenerator) GetDeleteDependencies(trigger schema.Trigger) ([]dependency, error) {
	deps := []dependency{
		mustRun(t.GetSQLVertexId(trigger, diffTypeDelete)).before(buildFunctionVertexId(trigger.Function, diffTypeDelete)),
		mustRun(t.GetSQLVertexId(trigger, diffTypeDelete)).before(buildTableVertexId(trigger.OwningTable, diffTypeDelete)),
	}
	// Run before the functions its WHEN condition calls are dropped.
	for _, f := range trigger.DependsOnFunctions {
		deps = append(deps, mustRun(t.GetSQLVertexId(trigger, diffTypeDelete)).before(buildFunctionVertexId(f, diffTypeDelete)))
	}
	return deps, nil
}
