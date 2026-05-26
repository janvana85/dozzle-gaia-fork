import type { EditorView as CodeMirrorEditorView, ViewUpdate } from "@codemirror/view";

export interface TemplateEditorOptions {
  parent: HTMLElement;
  initialValue: string;
  onChange?: (value: string) => void;
  language?: "json" | "plain";
}

export async function createTemplateEditor(options: TemplateEditorOptions) {
  const [
    { Decoration, EditorView, ViewPlugin },
    { EditorState },
    { json },
    { HighlightStyle, syntaxHighlighting },
    { tags },
  ] = await Promise.all([
    import("@codemirror/view"),
    import("@codemirror/state"),
    import("@codemirror/lang-json"),
    import("@codemirror/language"),
    import("@lezer/highlight"),
  ]);

  function buildTemplateVariableDecorations(view: CodeMirrorEditorView) {
    const ranges: { from: number; to: number; value: ReturnType<typeof Decoration.mark> }[] = [];
    const variableMark = Decoration.mark({ class: "cm-template-variable" });

    for (const { from, to } of view.visibleRanges) {
      const text = view.state.doc.sliceString(from, to);
      const variablePattern = /{{-?[\s\S]*?-?}}/g;
      let match: RegExpExecArray | null;

      while ((match = variablePattern.exec(text))) {
        ranges.push({
          from: from + match.index,
          to: from + match.index + match[0].length,
          value: variableMark,
        });
      }
    }

    return Decoration.set(ranges, true);
  }

  const templateVariableHighlight = ViewPlugin.fromClass(
    class {
      decorations;

      constructor(view: CodeMirrorEditorView) {
        this.decorations = buildTemplateVariableDecorations(view);
      }

      update(update: ViewUpdate) {
        if (update.docChanged || update.viewportChanged) {
          this.decorations = buildTemplateVariableDecorations(update.view);
        }
      }
    },
    {
      decorations: (plugin) => plugin.decorations,
    },
  );

  const editorTheme = EditorView.theme({
    "&": {
      backgroundColor: "var(--color-base-100)",
      color: "var(--color-base-content)",
      fontSize: "0.875rem",
    },
    ".cm-content": {
      caretColor: "var(--color-primary)",
      fontFamily: "ui-monospace, monospace",
    },
    ".cm-cursor": {
      borderLeftColor: "var(--color-primary)",
    },
    "&.cm-focused .cm-selectionBackground, .cm-selectionBackground": {
      backgroundColor: "var(--color-base-300)",
    },
    ".cm-activeLine": {
      backgroundColor: "color-mix(in oklch, var(--color-base-200) 50%, transparent)",
    },
    ".cm-gutters": {
      backgroundColor: "var(--color-base-200)",
      color: "color-mix(in oklch, var(--color-base-content) 50%, transparent)",
      border: "none",
    },
    ".cm-activeLineGutter": {
      backgroundColor: "var(--color-base-300)",
    },
    ".cm-template-variable": {
      color: "var(--color-primary)",
      fontWeight: "600",
      backgroundColor: "color-mix(in oklch, var(--color-primary) 12%, transparent)",
      borderRadius: "0.125rem",
    },
  });

  const highlightStyle = HighlightStyle.define([
    { tag: tags.propertyName, color: "var(--color-info)" },
    { tag: tags.string, color: "var(--color-success)" },
    { tag: tags.number, color: "var(--color-warning)" },
    { tag: tags.bool, color: "var(--color-warning)" },
    { tag: tags.null, color: "var(--color-secondary)" },
    { tag: tags.punctuation, color: "var(--color-base-content)" },
  ]);

  const state = EditorState.create({
    doc: options.initialValue,
    extensions: [
      EditorView.lineWrapping,
      ...(options.language === "plain" ? [] : [json()]),
      editorTheme,
      syntaxHighlighting(highlightStyle),
      templateVariableHighlight,
      EditorView.updateListener.of((update) => {
        if (update.docChanged && options.onChange) {
          options.onChange(update.view.state.doc.toString());
        }
      }),
    ],
  });

  return new EditorView({ state, parent: options.parent });
}
