package main

import (
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
)

// tableDataSource is a minimal NSTableViewDataSource: DarwInKit ships no
// settable data-source struct, so we implement the protocol ourselves. Only the
// row count is meaningful — cell content comes from the delegate's
// view-for-column callback — so every other method reports "not implemented"
// via its Has* guard and DarwInKit never exposes the selector to AppKit.
type tableDataSource struct {
	rowCount func() int
}

func (d *tableDataSource) NumberOfRowsInTableView(appkit.TableView) int { return d.rowCount() }

func (d *tableDataSource) HasNumberOfRowsInTableView() bool { return true }

func (d *tableDataSource) TableViewObjectValueForTableColumnRow(appkit.TableView, appkit.TableColumn, int) objc.Object {
	return objc.ObjectFrom(nil)
}
func (d *tableDataSource) HasTableViewObjectValueForTableColumnRow() bool { return false }

func (d *tableDataSource) TableViewSetObjectValueForTableColumnRow(appkit.TableView, objc.Object, appkit.TableColumn, int) {
}
func (d *tableDataSource) HasTableViewSetObjectValueForTableColumnRow() bool { return false }

func (d *tableDataSource) TableViewSortDescriptorsDidChange(appkit.TableView, []foundation.SortDescriptor) {
}
func (d *tableDataSource) HasTableViewSortDescriptorsDidChange() bool { return false }

func (d *tableDataSource) TableViewDraggingSessionEndedAtPointOperation(appkit.TableView, appkit.DraggingSession, foundation.Point, appkit.DragOperation) {
}
func (d *tableDataSource) HasTableViewDraggingSessionEndedAtPointOperation() bool { return false }

func (d *tableDataSource) TableViewDraggingSessionWillBeginAtPointForRowIndexes(appkit.TableView, appkit.DraggingSession, foundation.Point, foundation.IndexSet) {
}
func (d *tableDataSource) HasTableViewDraggingSessionWillBeginAtPointForRowIndexes() bool {
	return false
}

func (d *tableDataSource) TableViewAcceptDropRowDropOperation(appkit.TableView, appkit.DraggingInfoObject, int, appkit.TableViewDropOperation) bool {
	return false
}
func (d *tableDataSource) HasTableViewAcceptDropRowDropOperation() bool { return false }

func (d *tableDataSource) TableViewPasteboardWriterForRow(appkit.TableView, int) appkit.PasteboardWritingObject {
	return appkit.PasteboardWritingObject{}
}
func (d *tableDataSource) HasTableViewPasteboardWriterForRow() bool { return false }

func (d *tableDataSource) TableViewUpdateDraggingItemsForDrag(appkit.TableView, appkit.DraggingInfoObject) {
}
func (d *tableDataSource) HasTableViewUpdateDraggingItemsForDrag() bool { return false }

func (d *tableDataSource) TableViewValidateDropProposedRowProposedDropOperation(appkit.TableView, appkit.DraggingInfoObject, int, appkit.TableViewDropOperation) appkit.DragOperation {
	return 0
}
func (d *tableDataSource) HasTableViewValidateDropProposedRowProposedDropOperation() bool {
	return false
}
