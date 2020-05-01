package yara

/*
#include <stdlib.h>
#include <yara.h>
*/
import "C"
import (
	"reflect"
	"runtime"
	"unsafe"
)

// ScanContext contains the data passed to the ScanCallback methods.
//
// Since this type contains a C pointer to a YR_SCAN_CONTEXT structure
// that may be automatically freed, it should not be copied.
type ScanContext struct {
	cptr *C.YR_SCAN_CONTEXT
}

// ScanCallback is a placeholder for different interfaces that may be
// implemented by the callback object that is passed to the
// (*Rules).Scan*WithCallback and (*Scanner).Scan methods.
type ScanCallback interface{}

// ScanCallbackMatch is used to record rules that matched during a
// scan. The RuleMatching method corresponds to YARA's
// CALLBACK_MSG_RULE_MATCHING message.
type ScanCallbackMatch interface {
	RuleMatching(*ScanContext, *Rule) (bool, error)
}

// ScanCallbackNoMatch is used to record rules that did not match
// during a scan. The RuleNotMatching method corresponds to YARA's
// CALLBACK_MSG_RULE_NOT_MATCHING mssage.
type ScanCallbackNoMatch interface {
	RuleNotMatching(*ScanContext, *Rule) (bool, error)
}

// ScanCallbackFinished is used to signal that a scan has finished.
// The ScanFinished method corresponds to YARA's
// CALLBACK_MSG_SCAN_FINISHED message.
type ScanCallbackFinished interface {
	ScanFinished(*ScanContext) (bool, error)
}

// ScanCallbackModuleImport is used to provide data to a YARA module.
// The ImportModule method corresponds to YARA's
// CALLBACK_MSG_IMPORT_MODULE message.
type ScanCallbackModuleImport interface {
	ImportModule(*ScanContext, string) ([]byte, bool, error)
}

// ScanCallbackModuleImportFinished can be used to free resources that
// have been used in the ScanCallbackModuleImport implementation. The
// ModuleImported method corresponds to YARA's
// CALLBACK_MSG_MODULE_IMPORTED message.
type ScanCallbackModuleImportFinished interface {
	ModuleImported(*ScanContext, *Object) (bool, error)
}

// scanCallbackContainer is used by to pass a ScanCallback (and
// associated data) between ScanXxx methods and scanCallbackFunc(). It
// stores the public callback interface and a list of malloc()'d C
// pointers.
type scanCallbackContainer struct {
	ScanCallback
	cdata []unsafe.Pointer
}

// makeScanCallbackContainer sets up a scanCallbackContainer with a
// finalizer method that that frees any stored C pointers when the
// container is garbage-collected.
func makeScanCallbackContainer(sc ScanCallback) *scanCallbackContainer {
	c := &scanCallbackContainer{ScanCallback: sc, cdata: nil}
	runtime.SetFinalizer(c, (*scanCallbackContainer).finalize)
	return c
}

// addCPointer adds a C pointer that can later be freed using free().
func (c *scanCallbackContainer) addCPointer(p unsafe.Pointer) { c.cdata = append(c.cdata, p) }

// finalize frees stored C pointers
func (c *scanCallbackContainer) finalize() {
	for _, p := range c.cdata {
		C.free(p)
	}
	c.cdata = nil
	runtime.SetFinalizer(c, nil)
}

//export scanCallbackFunc
func scanCallbackFunc(ctx *C.YR_SCAN_CONTEXT, message C.int, messageData, userData unsafe.Pointer) C.int {
	cbc, ok := callbackData.Get(userData).(*scanCallbackContainer)
	s := &ScanContext{cptr: ctx}
	if !ok {
		return C.CALLBACK_ERROR
	}
	var abort bool
	var err error
	switch message {
	case C.CALLBACK_MSG_RULE_MATCHING:
		if c, ok := cbc.ScanCallback.(ScanCallbackMatch); ok {
			r := (*C.YR_RULE)(messageData)
			abort, err = c.RuleMatching(s, &Rule{r})
		}
	case C.CALLBACK_MSG_RULE_NOT_MATCHING:
		if c, ok := cbc.ScanCallback.(ScanCallbackNoMatch); ok {
			r := (*C.YR_RULE)(messageData)
			abort, err = c.RuleNotMatching(s, &Rule{r})
		}
	case C.CALLBACK_MSG_SCAN_FINISHED:
		if c, ok := cbc.ScanCallback.(ScanCallbackFinished); ok {
			abort, err = c.ScanFinished(s)
		}
	case C.CALLBACK_MSG_IMPORT_MODULE:
		if c, ok := cbc.ScanCallback.(ScanCallbackModuleImport); ok {
			mi := (*C.YR_MODULE_IMPORT)(messageData)
			var buf []byte
			if buf, abort, err = c.ImportModule(s, C.GoString(mi.module_name)); len(buf) == 0 {
				break
			}
			cbuf := C.calloc(1, C.size_t(len(buf)))
			outbuf := make([]byte, 0)
			hdr := (*reflect.SliceHeader)(unsafe.Pointer(&outbuf))
			hdr.Data, hdr.Len = uintptr(cbuf), len(buf)
			copy(outbuf, buf)
			mi.module_data, mi.module_data_size = unsafe.Pointer(&outbuf[0]), C.size_t(len(outbuf))
			cbc.addCPointer(cbuf)
		}
	case C.CALLBACK_MSG_MODULE_IMPORTED:
		if c, ok := cbc.ScanCallback.(ScanCallbackModuleImportFinished); ok {
			obj := (*C.YR_OBJECT)(messageData)
			abort, err = c.ModuleImported(s, &Object{obj})
		}
	}

	if err != nil {
		return C.CALLBACK_ERROR
	}
	if abort {
		return C.CALLBACK_ABORT
	}
	return C.CALLBACK_CONTINUE
}

// MatchRules is used to collect matches that are returned by the
// simple (*Rules).Scan* methods.
type MatchRules []MatchRule

// RuleMatching implements the ScanCallbackMatch interface for
// MatchRules.
func (mr *MatchRules) RuleMatching(sc *ScanContext, r *Rule) (abort bool, err error) {
	metas := r.Metas()
	// convert int to int32 for code that relies on previous behavior
	for s := range metas {
		if i, ok := metas[s].(int); ok {
			metas[s] = int32(i)
		}
	}
	*mr = append(*mr, MatchRule{
		Rule:      r.Identifier(),
		Namespace: r.Namespace(),
		Tags:      r.Tags(),
		Meta:      metas,
		Strings:   r.getMatchStrings(sc),
	})
	return
}
