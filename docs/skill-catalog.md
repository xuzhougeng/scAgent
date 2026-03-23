# Skill Catalog

This catalog organizes common single-cell analysis skills by workflow stage.

`support_level`

- `wired`: already accepted by the current execution chain
- `planned`: schema is defined, but the runtime handler is not implemented yet

## Session

- `inspect_dataset` (`wired`): summarize the focused object and available annotations
- `assess_dataset` (`wired`): evaluate whether the uploaded h5ad is raw-like, partially processed, or analysis-ready
- `set_focus_object` (`planned`): switch the focused object

## Quality Control

- `summarize_qc` (`wired`): calculate standard QC metrics
- `plot_qc_metrics` (`wired`): visualize QC distributions
- `filter_cells` (`wired`): filter cells by QC thresholds
- `filter_genes` (`wired`): filter genes by detection thresholds

## Preprocessing

- `normalize_total` (`wired`): library-size normalization
- `log1p_transform` (`wired`): log1p transform counts
- `select_hvg` (`wired`): select highly variable genes
- `scale_matrix` (`wired`): scale expression matrix

## Embedding

- `run_pca` (`wired`): compute principal components
- `compute_neighbors` (`wired`): build neighbor graph
- `run_umap` (`wired`): compute UMAP embedding
- `prepare_umap` (`wired`): run the preprocessing chain needed before UMAP

## Subsetting And Scoring

- `subset_cells` (`wired`): filter cells into a child object
- `score_gene_set` (`wired`): score marker/gene signatures

## Clustering

- `recluster` (`wired`): recluster the focused object
- `subcluster_group` (`wired`): isolate one cluster or cell group and recluster only that subgroup
- `rename_clusters` (`wired`): rename cluster labels

## Annotation

- `annotate_cell_types` (`planned`): assign cell-type labels

## Differential Expression

- `find_markers` (`wired`): generate marker table by group
- `differential_expression` (`planned`): compare two groups

## Visualization

- `plot_umap` (`wired`): UMAP overview with configurable params such as `color_by`, `legend_loc`, `title`, and figure size
- `plot_gene_umap` (`wired`): UMAP colored by gene expression
- `plot_dotplot` (`wired`): marker or gene dotplot
- `plot_violin` (`wired`): violin plot for genes
- `plot_heatmap` (`wired`): heatmap for selected genes
- `plot_celltype_composition` (`wired`): cell-type composition by sample or condition

## Custom

- `run_python_analysis` (`wired`): execute a short Python snippet against the in-memory `adata` object when no existing tool is expressive enough

## Integration

- `batch_integrate` (`planned`): batch correction or integration

## Export

- `export_h5ad` (`wired`): materialize current object as `.h5ad`
- `export_markers_csv` (`wired`): export result table as CSV

## Suggested Implementation Order

If you want to extend the runtime with new real analysis skills, the best order is:

1. `inspect_dataset`
2. `filter_cells`
3. `normalize_total`
4. `select_hvg`
5. `run_pca`
6. `compute_neighbors`
7. `run_umap`
8. `plot_gene_umap`
9. `subset_cells`
10. `find_markers`
