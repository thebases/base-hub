# UI invisible taf Logic

app/frontend-admin/src/components/views/form/form-arch-parser.ts (line 389) and form-arch-eval.ts (line 8) show that the frontend does handle form-view invisible as a real expression, not just a static flag. The parser keeps the raw invisible string from XML, and the evaluator converts Python-like syntax such as and, or, not, in, not in, True, False, None into JS and runs it against the current form record. Then form-renderer.tsx (line 304) drops any node whose invisible resolves truthy, and it re-runs this on every form value change through the form.Subscribe render path at form-renderer.tsx (line 1065). So for form ui.view, record-driven invisibility like state != 'draft' is live and reactive.

app/frontend-admin/src/components/views/form/form-renderer.tsx (line 943) has a gap for context-based invisibility. The evaluator supports context, context.get(...), uid, and user_id in form-arch-eval.ts (line 42), but the renderer only passes evalContext: { record: values }, never a real context. That means expressions like 'default_privilege_id' in context or context.get('is_action_tbl_users') will evaluate against {} and give the wrong visibility result. Concrete fix: pass action/view context from the controller into FormRenderer, then into evalContext, and use the same context during validation in collectVisibleArchFields.

app/frontend-admin/src/api/jsonv2.ts (line 2091) and list-renderer.tsx (line 183) show a second gap: list views do not handle dynamic invisible expressions. parseListFieldNodes only accepts literal boolean values via readArchBoolean, so invisible="state != 'draft'" becomes undefined and the column stays visible. I also found no frontend support for column_invisible at all. Concrete fix: either parse list-field invisibility as an expression string and evaluate it per row or per parent context, or explicitly support column_invisible in the list arch parser and column builder.

Nit

app/frontend-admin/src/components/views/form/form-arch-parser.ts (line 393) preserves raw attrs, but I did not find frontend code that interprets legacy attrs="{'invisible': ...}" modifiers. If your backend arches still rely on attrs instead of direct invisible="...", those rules will be ignored unless the widget handles them itself.

So the short answer is: form ui.view invisibility is handled on the frontend and re-evaluated from current form values, but list-view invisibility is only static, and context-based invisibility is currently incomplete.
