// initChart creates a Chart.js bar chart on the given canvas element.
// canvasId: the DOM id of the <canvas> element
// labels:   array of x-axis labels
// data:     array of numeric values
// label:    dataset label string
function initChart(canvasId, labels, data, label) {
    var ctx = document.getElementById(canvasId);
    if (!ctx) return null;

    return new Chart(ctx, {
        type: 'bar',
        data: {
            labels: labels,
            datasets: [{
                label: label,
                data: data,
                backgroundColor: 'rgba(239, 68, 68, 0.7)',
                borderRadius: 3,
            }]
        },
        options: {
            responsive: true,
            plugins: { legend: { display: false } },
            scales: {
                x: { grid: { display: false } },
                y: { beginAtZero: true, grid: { color: '#f3f4f6' } },
            },
        }
    });
}
