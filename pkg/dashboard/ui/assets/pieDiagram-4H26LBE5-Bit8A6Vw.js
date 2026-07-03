import{n as e,r as t}from"./chunk-AGHRB4JF-D1M2UVIx.js";import{i as n,p as r}from"./chunk-5ZQYHXKU-CKdNQHhk.js";import{a as i,p as a,r as o}from"./index-BOTG1WMm.js";import{t as s}from"./mermaid-parser.core-BcI58RSR.js";import{H as c,K as l,U as u,a as d,c as f,f as p,v as m,w as h,x as g,y as _}from"./chunk-CSCIHK7Q-B1TYg1ev.js";import{t as v}from"./chunk-WU5MYG2G-BPJXJcJx.js";import{t as y}from"./chunk-4BX2VUAB-DjlzBwy3.js";var b=p.pie,x={sections:new Map,showData:!1,config:b},S=x.sections,C=x.showData,w=structuredClone(b),T={getConfig:e(()=>structuredClone(w),`getConfig`),clear:e(()=>{S=new Map,C=x.showData,d()},`clear`),setDiagramTitle:l,getDiagramTitle:h,setAccTitle:u,getAccTitle:_,setAccDescription:c,getAccDescription:m,addSection:e(({label:e,value:n})=>{if(n<0)throw Error(`"${e}" has invalid value: ${n}. Negative values are not allowed in pie charts. All slice values must be >= 0.`);S.has(e)||(S.set(e,n),t.debug(`added new section: ${e}, with value: ${n}`))},`addSection`),getSections:e(()=>S,`getSections`),setShowData:e(e=>{C=e},`setShowData`),getShowData:e(()=>C,`getShowData`)},E=e((e,t)=>{y(e,t),t.setShowData(e.showData),e.sections.map(t.addSection)},`populateDb`),D={parse:e(async e=>{let n=await s(`pie`,e);t.debug(n),E(n,T)},`parse`)},O=e(e=>`
  .pieCircle{
    stroke: ${e.pieStrokeColor};
    stroke-width : ${e.pieStrokeWidth};
    opacity : ${e.pieOpacity};
  }
  .pieOuterCircle{
    stroke: ${e.pieOuterStrokeColor};
    stroke-width: ${e.pieOuterStrokeWidth};
    fill: none;
  }
  .pieTitleText {
    text-anchor: middle;
    font-size: ${e.pieTitleTextSize};
    fill: ${e.pieTitleTextColor};
    font-family: ${e.fontFamily};
  }
  .slice {
    font-family: ${e.fontFamily};
    fill: ${e.pieSectionTextColor};
    font-size:${e.pieSectionTextSize};
    // fill: white;
  }
  .legend text {
    fill: ${e.pieLegendTextColor};
    font-family: ${e.fontFamily};
    font-size: ${e.pieLegendTextSize};
  }
`,`getStyles`),k=e(e=>{let t=[...e.values()].reduce((e,t)=>e+t,0),n=[...e.entries()].map(([e,t])=>({label:e,value:t})).filter(e=>e.value/t*100>=1);return o().value(e=>e.value).sort(null)(n)},`createPieArcs`),A={parser:D,db:T,renderer:{draw:e((e,o,s,c)=>{t.debug(`rendering pie chart
`+e);let l=c.db,u=g(),d=n(l.getConfig(),u.pie),p=v(o),m=p.append(`g`);m.attr(`transform`,`translate(225,225)`);let{themeVariables:h}=u,[_]=r(h.pieOuterStrokeWidth);_??=2;let y=d.textPosition,b=i().innerRadius(0).outerRadius(185),x=i().innerRadius(185*y).outerRadius(185*y);m.append(`circle`).attr(`cx`,0).attr(`cy`,0).attr(`r`,185+_/2).attr(`class`,`pieOuterCircle`);let S=l.getSections(),C=k(S),w=[h.pie1,h.pie2,h.pie3,h.pie4,h.pie5,h.pie6,h.pie7,h.pie8,h.pie9,h.pie10,h.pie11,h.pie12],T=0;S.forEach(e=>{T+=e});let E=C.filter(e=>(e.data.value/T*100).toFixed(0)!==`0`),D=a(w).domain([...S.keys()]);m.selectAll(`mySlices`).data(E).enter().append(`path`).attr(`d`,b).attr(`fill`,e=>D(e.data.label)).attr(`class`,`pieCircle`),m.selectAll(`mySlices`).data(E).enter().append(`text`).text(e=>(e.data.value/T*100).toFixed(0)+`%`).attr(`transform`,e=>`translate(`+x.centroid(e)+`)`).style(`text-anchor`,`middle`).attr(`class`,`slice`);let O=m.append(`text`).text(l.getDiagramTitle()).attr(`x`,0).attr(`y`,-400/2).attr(`class`,`pieTitleText`),A=[...S.entries()].map(([e,t])=>({label:e,value:t})),j=m.selectAll(`.legend`).data(A).enter().append(`g`).attr(`class`,`legend`).attr(`transform`,(e,t)=>{let n=22*A.length/2;return`translate(216,`+(t*22-n)+`)`});j.append(`rect`).attr(`width`,18).attr(`height`,18).style(`fill`,e=>D(e.label)).style(`stroke`,e=>D(e.label)),j.append(`text`).attr(`x`,22).attr(`y`,14).text(e=>l.getShowData()?`${e.label} [${e.value}]`:e.label);let M=512+Math.max(...j.selectAll(`text`).nodes().map(e=>e?.getBoundingClientRect().width??0)),N=O.node()?.getBoundingClientRect().width??0,P=450/2-N/2,F=450/2+N/2,I=Math.min(0,P),L=Math.max(M,F)-I;p.attr(`viewBox`,`${I} 0 ${L} 450`),f(p,450,L,d.useMaxWidth)},`draw`)},styles:O};export{A as diagram};