const template = await (await fetch(new URL("./LegacyStyled.xml", import.meta.url))).text();

export class LegacyStyled extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
