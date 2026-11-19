const template = await (await fetch(new URL("./AboutTr_vi.xml", import.meta.url))).text();

export class AboutTr_vi extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
