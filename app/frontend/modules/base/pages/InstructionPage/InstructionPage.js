// Instruction page (/web/instruction): lists the kernel's and installed modules' instruction markdown
// (/doc/instructions.json) and renders the selected document (/doc/instruction.md).

import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { getApiDocLanguage, getInstruction, getInstructionIndex } from "@/modules/base/api/apidoc.js";
import { Alert, Card, CardBody, Markdown, Spinner } from "nodexui/component";

const template = await loadTemplate(new URL("./InstructionPage.xml", import.meta.url));

export class InstructionPage extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Card, CardBody, Markdown, Spinner };

  setup() {
    this.state = useState({
      lang: getApiDocLanguage(),
      status: "loading",
      message: "",
      modules: [],
      module: "",
      path: "",
      content: "",
      docStatus: "idle",
      docMessage: "",
    });
    onWillStart(() => this.loadIndex());
  }

  async loadIndex() {
    try {
      const payload = await getInstructionIndex(this.state.lang);
      this.state.modules = payload?.modules || [];
      this.state.status = "ready";
      const first = this.state.modules[0]?.items?.[0];
      if (first) await this.select(this.state.modules[0].module, first.path);
    } catch (error) {
      this.state.status = "error";
      this.state.message = error instanceof Error ? error.message : t("Failed to load instructions");
    }
  }

  isSelected(module, path) {
    return this.state.module === module && this.state.path === path;
  }

  async select(module, path) {
    this.state.module = module;
    this.state.path = path;
    this.state.docStatus = "loading";
    try {
      this.state.content = await getInstruction(module, path, this.state.lang);
      this.state.docStatus = "ready";
    } catch (error) {
      this.state.docStatus = "error";
      this.state.docMessage = error instanceof Error ? error.message : t("Failed to load the document");
    }
  }
}
