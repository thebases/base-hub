// Popup that shows the form view of the record a many2one points at (the field's "view" button).
// Loaded lazily by FormField (dynamic import) so FormView <-> FormField never import each other
// statically and the dialog stays off the cold-load path. Props: model, recordId, onClose.
import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";
import { Alert, Dialog, DialogContent } from "nodexui/component";
import { createRemoteOnchangeHandler, fetchModelFormRecord, fetchViewMetadataForType, fieldDefsFromFields, writeModelRecord } from "@/services/api/jsonv2.js";
import { FormView } from "./FormView.js";

const template = await loadTemplate(new URL("./RelationRecordDialog.xml", import.meta.url));

export class RelationRecordDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Dialog, DialogContent, FormView };

  setup() {
    this.state = useState({ viewData: null, fields: [], record: null, error: "" });
    this.remoteChange = undefined;
    onWillStart(() => this.load());
  }

  async load() {
    const { model, recordId } = this.props;
    try {
      const [viewData, read] = await Promise.all([fetchViewMetadataForType("form", model), fetchModelFormRecord(model, Number(recordId))]);
      const fields = fieldDefsFromFields(read.fields);
      this.remoteChange = createRemoteOnchangeHandler(model, Object.fromEntries(fields.map((field) => [field.name, field])));
      Object.assign(this.state, { viewData, fields, record: read.record });
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : "Failed to open record";
    }
  }

  async save(values) {
    const { model, recordId } = this.props;
    await writeModelRecord(model, Number(recordId), values);
    this.state.record = (await fetchModelFormRecord(model, Number(recordId))).record;
  }
}
